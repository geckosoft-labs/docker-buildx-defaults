// A docker-buildx that wraps the real one, to add the shared layer cache to
// the builds of Docker Compose (see README.md). Compose builds by running
// `docker-buildx bake --file -` with the build definition on stdin: the
// wrapper adds a cache-from and cache-to to each target that comes from a
// Compose service, and passes everything else through unchanged.
package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strings"
	"syscall"
	"time"
)

// Set by the release build (-ldflags "-X main.version=...")
var version = "dev"

const (
	// Answered by the wrapper itself: the install scripts use it to tell the
	// wrapper from the real docker-buildx
	versionArg = "docker-buildx-defaults-version"
	// Where the install scripts move a real docker-buildx found in the user's
	// plugin directory, under the Docker config directory; not in
	// cli-plugins, where the docker CLI would list it as an invalid plugin
	realDir = "docker-buildx-defaults"
)

func main() {
	args := os.Args[1:]
	if len(args) == 1 && args[0] == versionArg {
		fmt.Println("docker-buildx-defaults", version)
		return
	}
	buildx, err := findBuildx()
	if err != nil {
		fail(err)
	}
	// The docker CLI runs a plugin as `docker-buildx buildx <command>`,
	// Compose as `docker-buildx <command>`
	cmd := args
	if len(cmd) > 0 && cmd[0] == "buildx" {
		cmd = cmd[1:]
	}
	if len(cmd) == 0 || cmd[0] != "bake" || !readsStdin(cmd[1:]) {
		os.Exit(run(buildx, args, os.Stdin))
	}
	def, err := io.ReadAll(os.Stdin)
	if err != nil {
		fail(fmt.Errorf("reading the bake definition: %w", err))
	}
	os.Exit(run(buildx, append(args, cacheSets(def)...), bytes.NewReader(def)))
}

func fail(err error) {
	fmt.Fprintln(os.Stderr, "docker-buildx-defaults:", err)
	os.Exit(1)
}

func warn(format string, a ...any) {
	fmt.Fprintf(os.Stderr, "docker-buildx-defaults: "+format+"\n", a...)
}

// readsStdin tells whether bake reads its definition from stdin (--file -)
func readsStdin(args []string) bool {
	for i, a := range args {
		if a == "--file=-" || a == "-f=-" {
			return true
		}
		if (a == "--file" || a == "-f") && i+1 < len(args) && args[i+1] == "-" {
			return true
		}
	}
	return false
}

// cacheSets returns the --set arguments that add the cache to the targets of
// the bake definition that come from a Compose service
func cacheSets(def []byte) []string {
	from, to := locations()
	if len(from) == 0 && to == "" {
		return nil
	}
	var d struct {
		Target map[string]struct {
			Context   string            `json:"context"`
			Labels    map[string]string `json:"labels"`
			Platforms []string          `json:"platforms"`
		} `json:"target"`
	}
	if err := json.Unmarshal(def, &d); err != nil {
		// Not the JSON Compose writes (its format changed?): build without
		// the cache rather than not at all
		warn("not a bake definition from Compose, building without the cache: %v", err)
		return nil
	}
	names := make([]string, 0, len(d.Target))
	for name, t := range d.Target {
		if t.Labels["com.docker.compose.service"] != "" {
			names = append(names, name)
		}
	}
	if len(names) == 0 {
		return nil
	}
	sort.Strings(names)
	// Where a registry can't be reached (or when it's down) every import and
	// export there would fail, each with an ERROR line in the build output,
	// and each after a timeout: check each registry once, and say so once
	up := map[string]bool{}
	usable := func(location string) bool {
		host, _, _ := strings.Cut(location, "/")
		if ok, checked := up[host]; checked {
			return ok
		}
		err := reachable(host)
		if err != nil {
			warn("%s unreachable, building without the cache there: %v", host, err)
		}
		up[host] = err == nil
		return up[host]
	}
	var reachableFrom []string
	for _, l := range from {
		if usable(l) {
			reachableFrom = append(reachableFrom, l)
		}
	}
	from = reachableFrom
	if to != "" && !usable(to) {
		to = ""
	}
	var sets []string
	repos := map[string]checkout{}
	for _, name := range names {
		t := d.Target[name]
		c, ok := repos[t.Context]
		if !ok {
			c = inspect(t.Context)
			repos[t.Context] = c
		}
		if c.repo == "" {
			continue // not a git checkout with an origin: nothing to share it with
		}
		// One ref per platform: an export replaces the whole ref, so machines
		// of different platforms building the same branch would replace each
		// other's cache, which is of no use to the other
		image := c.repo + "/" + pathComponent(t.Labels["com.docker.compose.service"]) + "/" + platformPath(t.Platforms)
		ref := func(location, branch string) string {
			return location + "/" + image + ":" + tag(branch)
		}
		// The branch's caches first, then the default branch's
		var refs []string
		if c.branch != "" {
			for _, l := range from {
				refs = append(refs, ref(l, c.branch))
			}
		}
		if c.defaultBranch != c.branch {
			for _, l := range from {
				refs = append(refs, ref(l, c.defaultBranch))
			}
		}
		for _, r := range refs {
			sets = append(sets, "--set", name+".cache-from+=type=registry,ref="+r)
		}
		if to != "" && c.branch != "" {
			// ignore-error: a failed upload doesn't fail the build.
			// image-manifest: a plain OCI image manifest, the default of
			// recent BuildKit, for older ones too (instead of an index that
			// lists layers), so every cache in the registry has the same shape
			sets = append(sets, "--set", name+".cache-to+=type=registry,ref="+ref(to, c.branch)+",mode=max,ignore-error=true,image-manifest=true,oci-mediatypes=true")
		}
	}
	return sets
}

// locations are where the cache is imported from (DOCKER_BUILDX_DEFAULTS_CACHE_FROM,
// comma-separated, in order) and exported to (DOCKER_BUILDX_DEFAULTS_CACHE_TO, none
// if empty): each a registry with an optional path, host[:port][/path], e.g.
// registry.example/dev. Who reads and writes where is up to the registry's
// access control (see README.md).
func locations() (from []string, to string) {
	for _, l := range strings.Split(os.Getenv("DOCKER_BUILDX_DEFAULTS_CACHE_FROM"), ",") {
		if l = location(l); l != "" {
			from = append(from, l)
		}
	}
	return from, location(os.Getenv("DOCKER_BUILDX_DEFAULTS_CACHE_TO"))
}

func location(s string) string {
	return strings.TrimRight(strings.TrimSpace(s), "/")
}

// platformPath names the platforms a target is built for as one path
// component: linux/arm64 is linux-arm64, linux/amd64 and linux/arm64 together
// linux-amd64_linux-arm64. A target that names none is built for the
// default one: DOCKER_DEFAULT_PLATFORM, or the native one, which is Linux on
// the machine's architecture (Docker Desktop runs a Linux VM of its own
// architecture). A remote daemon of another architecture makes the guess
// wrong: that costs cache hits, not correctness, as the platform is part of
// BuildKit's cache keys.
func platformPath(platforms []string) string {
	if len(platforms) == 0 {
		if p := os.Getenv("DOCKER_DEFAULT_PLATFORM"); p != "" {
			platforms = strings.Split(p, ",")
		} else {
			platforms = []string{"linux/" + runtime.GOARCH}
		}
	}
	names := make([]string, len(platforms))
	for i, p := range platforms {
		p = strings.ToLower(strings.TrimSpace(p))
		if strings.HasSuffix(p, "/arm64/v8") {
			p = strings.TrimSuffix(p, "/v8") // the same platform, as Docker has it
		}
		names[i] = pathComponent(strings.ReplaceAll(p, "/", "-"))
	}
	sort.Strings(names)
	return strings.Join(names, "_")
}

func reachable(registry string) error {
	addr := registry
	if _, _, err := net.SplitHostPort(registry); err != nil {
		addr = net.JoinHostPort(registry, "443")
	}
	conn, err := net.DialTimeout("tcp", addr, time.Second)
	if err != nil {
		return err
	}
	return conn.Close()
}

type checkout struct {
	repo          string // <org>/<repo> of the origin remote, as a registry path
	branch        string // "" when detached
	defaultBranch string
}

func inspect(dir string) checkout {
	if fi, err := os.Stat(dir); err != nil || !fi.IsDir() {
		return checkout{} // a remote context (a URL), not a local checkout
	}
	var c checkout
	url, ok := git(dir, "remote", "get-url", "origin")
	if !ok {
		return c
	}
	c.repo = repoPath(url)
	// Unknown when the clone didn't record origin's HEAD (`git remote
	// set-head origin --auto` fixes it), as in CI's shallow checkouts: main is
	// the usual one
	c.defaultBranch = "main"
	if head, ok := git(dir, "symbolic-ref", "--quiet", "--short", "refs/remotes/origin/HEAD"); ok {
		c.defaultBranch = strings.TrimPrefix(head, "origin/")
	}
	c.branch = branch(dir, c.defaultBranch)
	return c
}

// branch is the branch being built, "" if unknown. CI checks out a commit
// (detached HEAD): TeamCity's builds name the branch in DOCKER_BUILDX_DEFAULTS_BRANCH
// (see README.md), GitHub's jobs in their own variables.
func branch(dir, defaultBranch string) string {
	b := os.Getenv("DOCKER_BUILDX_DEFAULTS_BRANCH")
	if b == "" {
		b, _ = git(dir, "symbolic-ref", "--quiet", "--short", "HEAD")
	}
	if b == "" {
		b = os.Getenv("GITHUB_HEAD_REF") // the source branch of a pull request
	}
	// Not a tag's name
	if b == "" && os.Getenv("GITHUB_REF_TYPE") == "branch" {
		b = os.Getenv("GITHUB_REF_NAME")
	}
	b = strings.TrimPrefix(b, "refs/heads/")
	if b == "<default>" { // TeamCity's name for the default branch
		b = defaultBranch
	}
	return b
}

// git runs a git command in dir: its trimmed output, and whether it succeeded
// (failures, like a missing remote, are answers here, not errors)
func git(dir string, args ...string) (string, bool) {
	out, err := exec.Command("git", append([]string{"-C", dir}, args...)...).Output()
	if err != nil {
		return "", false
	}
	return strings.TrimSpace(string(out)), true
}

// repoPath turns a remote URL (git@github.com:org/repo.git,
// https://github.com/org/repo, ...) into org/repo
func repoPath(url string) string {
	url = strings.TrimSuffix(strings.TrimSuffix(url, "/"), ".git")
	parts := strings.FieldsFunc(url, func(r rune) bool { return r == '/' || r == ':' })
	if len(parts) < 2 {
		return ""
	}
	org, repo := pathComponent(parts[len(parts)-2]), pathComponent(parts[len(parts)-1])
	if org == "" || repo == "" {
		return ""
	}
	return org + "/" + repo
}

var (
	notInPath = regexp.MustCompile(`[^a-z0-9._-]+`)
	notInTag  = regexp.MustCompile(`[^A-Za-z0-9_.-]+`)
)

// pathComponent makes s valid in a registry path: lowercase letters, digits
// and separators, which can't start or end it
func pathComponent(s string) string {
	return strings.Trim(notInPath.ReplaceAllString(strings.ToLower(s), "-"), "._-")
}

// tag makes a branch name a valid tag: feature/foo is feature-foo
func tag(branch string) string {
	t := notInTag.ReplaceAllString(branch, "-")
	if t == "" || t[0] == '.' || t[0] == '-' {
		t = "_" + t
	}
	if len(t) > 128 {
		t = t[:128]
	}
	return t
}

// findBuildx finds the real docker-buildx: the one an install script moved aside,
// or the first one where the docker CLI looks for plugins, after the user's
// directory (where the wrapper is)
func findBuildx() (string, error) {
	name := "docker-buildx"
	if runtime.GOOS == "windows" {
		name += ".exe"
	}
	if p := os.Getenv("DOCKER_BUILDX_DEFAULTS_BUILDX"); p != "" {
		if _, err := os.Stat(p); err != nil {
			return "", fmt.Errorf("DOCKER_BUILDX_DEFAULTS_BUILDX: %w", err)
		}
		return p, nil
	}
	self, err := os.Executable()
	if err != nil {
		return "", err
	}
	if self, err = filepath.EvalSymlinks(self); err != nil {
		return "", err
	}
	config := os.Getenv("DOCKER_CONFIG")
	if config == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", err
		}
		config = filepath.Join(home, ".docker")
	}
	extra, err := extraPluginDirs(config)
	if err != nil {
		return "", err
	}
	dirs := append([]string{filepath.Join(config, realDir)}, extra...)
	dirs = append(dirs, systemPluginDirs()...)
	for _, dir := range dirs {
		p := filepath.Join(dir, name)
		resolved, err := filepath.EvalSymlinks(p)
		if err != nil {
			continue // not there
		}
		if resolved != self {
			return p, nil
		}
	}
	return "", fmt.Errorf("no docker-buildx found besides this wrapper (in %s); set DOCKER_BUILDX_DEFAULTS_BUILDX to its path", strings.Join(dirs, ", "))
}

// extraPluginDirs reads cliPluginsExtraDirs from the docker CLI config, which
// is where Docker Desktop points to its plugins
func extraPluginDirs(config string) ([]string, error) {
	b, err := os.ReadFile(filepath.Join(config, "config.json"))
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var c struct {
		Dirs []string `json:"cliPluginsExtraDirs"`
	}
	if err := json.Unmarshal(b, &c); err != nil {
		return nil, fmt.Errorf("%s: %w", filepath.Join(config, "config.json"), err)
	}
	return c.Dirs, nil
}

// systemPluginDirs are where the docker CLI looks for plugins after the
// user's directory and the extra ones, plus where Docker Desktop and Homebrew
// install them
func systemPluginDirs() []string {
	switch runtime.GOOS {
	case "windows":
		return []string{
			filepath.Join(os.Getenv("ProgramData"), "Docker", "cli-plugins"),
			filepath.Join(os.Getenv("ProgramFiles"), "Docker", "cli-plugins"),
			filepath.Join(os.Getenv("ProgramFiles"), "Docker", "Docker", "resources", "cli-plugins"),
		}
	case "darwin":
		return []string{
			"/Applications/Docker.app/Contents/Resources/cli-plugins",
			"/opt/homebrew/lib/docker/cli-plugins",
			"/usr/local/lib/docker/cli-plugins",
			"/usr/local/libexec/docker/cli-plugins",
		}
	default:
		return []string{
			"/usr/local/lib/docker/cli-plugins",
			"/usr/local/libexec/docker/cli-plugins",
			"/usr/lib/docker/cli-plugins",
			"/usr/libexec/docker/cli-plugins",
		}
	}
}

// run runs the real docker-buildx and returns its exit code
func run(buildx string, args []string, stdin io.Reader) int {
	cmd := exec.Command(buildx, args...)
	cmd.Stdin, cmd.Stdout, cmd.Stderr = stdin, os.Stdout, os.Stderr
	if err := cmd.Start(); err != nil {
		fail(err)
	}
	// Pass on Ctrl+C and termination. Signal fails once the child has exited
	// (and on Windows for anything but Kill, where Ctrl+C reaches the whole
	// console anyway): nothing to pass on then
	sigs := make(chan os.Signal, 1)
	signal.Notify(sigs, os.Interrupt, syscall.SIGTERM)
	go func() {
		for s := range sigs {
			_ = cmd.Process.Signal(s)
		}
	}()
	err := cmd.Wait()
	var exit *exec.ExitError
	if errors.As(err, &exit) {
		return exit.ExitCode()
	}
	if err != nil {
		fail(err)
	}
	return 0
}
