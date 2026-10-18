// A docker-buildx that wraps the real one, to give the builds of Docker
// Compose the defaults Compose has no global setting for (see README.md):
// zstd compression, and a layer cache shared through a registry. Compose builds
// by running `docker-buildx bake --file -` with the build definition on
// stdin: the wrapper adds its settings to each target that comes from a
// Compose service, and passes everything else through unchanged.
package main

import (
	"bufio"
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
	"strconv"
	"strings"
	"syscall"
	"time"
)

// Set by the release build (-ldflags "-X main.version=...")
var version = "dev"

const (
	name = "docker-buildx-defaults"
	// Answered by the wrapper itself: the install scripts use it to tell the
	// wrapper from the real docker-buildx
	versionArg = name + "-version"
	// `docker buildx docker-buildx-defaults`: the settings in effect, and
	// where each comes from
	settingsArg = name
	// Where the install scripts move a real docker-buildx found in the user's
	// plugin directory, under the Docker config directory; not in
	// cli-plugins, where the docker CLI would list it as an invalid plugin
	realDir = name
	// Of the environment variables; the config file has the same names
	envPrefix = "DOCKER_BUILDX_DEFAULTS_"
)

// The settings, without envPrefix, with their defaults; an empty value is the
// default. The branch is the build's, not the user's: not in the config file.
var settingDefs = []struct {
	name, def string
	inFile    bool
}{
	{"COMPRESSION", "zstd", true},
	{"CACHE_FROM", "", true},
	{"CACHE_TO", "", true},
	{"BRANCH", "", false},
	{"BUILDX", "", true},
}

func main() {
	args := os.Args[1:]
	if len(args) == 1 && args[0] == versionArg {
		fmt.Println(name, version)
		return
	}
	s, err := loadSettings(configFile())
	// The docker CLI asks every plugin for its metadata first, and takes one
	// that fails it for a missing plugin, without its error: pass it through
	// (to a real docker-buildx found without the settings), so that the
	// command itself fails, with the reason
	if err != nil && len(args) == 1 && args[0] == "docker-cli-plugin-metadata" {
		s = settings{values: map[string]setting{}}
	} else if err != nil {
		fail(err)
	}
	// The docker CLI runs a plugin as `docker-buildx buildx <command>`,
	// Compose as `docker-buildx <command>`
	cmd := args
	if len(cmd) > 0 && cmd[0] == "buildx" {
		cmd = cmd[1:]
	}
	buildx, buildxErr := findBuildx(s)
	if len(cmd) == 1 && cmd[0] == settingsArg {
		printSettings(s, buildx, buildxErr)
		return
	}
	if buildxErr != nil {
		fail(buildxErr)
	}
	if len(cmd) == 0 || cmd[0] != "bake" || !readsStdin(cmd[1:]) {
		os.Exit(run(buildx, args, os.Stdin))
	}
	def, err := io.ReadAll(os.Stdin)
	if err != nil {
		fail(fmt.Errorf("reading the bake definition: %w", err))
	}
	os.Exit(run(buildx, append(args, defaultSets(def, s)...), bytes.NewReader(def)))
}

func fail(err error) {
	fmt.Fprintln(os.Stderr, name+":", err)
	os.Exit(1)
}

func warn(format string, a ...any) {
	fmt.Fprintf(os.Stderr, name+": "+format+"\n", a...)
}

type setting struct {
	value  string
	source string // "environment", the config file's path, or "default"
}

type settings struct {
	file   string             // the config file, "" when there's no config directory
	values map[string]setting // by name, without envPrefix
}

func (s settings) get(name string) string { return s.values[name].value }

// configFile is the user's config file, e.g.
// ~/.config/docker-buildx-defaults/config on Linux; "" when there's no
// config directory (no HOME, as for some services)
func configFile() string {
	dir, err := os.UserConfigDir()
	if err != nil {
		return ""
	}
	return filepath.Join(dir, name, "config")
}

// loadSettings reads each setting from the environment (when set, even
// empty), else from the config file (when it has it), else its default
func loadSettings(file string) (settings, error) {
	s := settings{file: file, values: map[string]setting{}}
	inFile := map[string]string{}
	if file != "" {
		b, err := os.ReadFile(file)
		if err != nil && !errors.Is(err, os.ErrNotExist) {
			return s, err
		}
		if inFile, err = parseConfig(b, file); err != nil {
			return s, err
		}
	}
	for _, d := range settingDefs {
		v := setting{d.def, "default"}
		if fv, ok := inFile[d.name]; ok && fv != "" {
			v = setting{fv, file}
		}
		if ev, ok := os.LookupEnv(envPrefix + d.name); ok {
			v = setting{ev, "environment"}
			if ev == "" {
				v.value = d.def
			}
		}
		s.values[d.name] = v
	}
	if c := s.values["COMPRESSION"]; c.value != "gzip" && c.value != "zstd" {
		return s, fmt.Errorf("%sCOMPRESSION (%s): %q, not gzip or zstd", envPrefix, c.source, c.value)
	}
	return s, nil
}

// parseConfig reads KEY=VALUE lines, with the names of the environment
// variables; blank lines and lines starting with # are skipped
func parseConfig(b []byte, file string) (map[string]string, error) {
	values := map[string]string{}
	b = bytes.TrimPrefix(b, []byte("\xef\xbb\xbf")) // as some Windows editors write it
	sc := bufio.NewScanner(bytes.NewReader(b))
	for n := 1; sc.Scan(); n++ {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		key, value, ok := strings.Cut(line, "=")
		if !ok {
			return nil, fmt.Errorf("%s:%d: not KEY=VALUE", file, n)
		}
		key = strings.TrimSpace(key)
		known := false
		for _, d := range settingDefs {
			if key == envPrefix+d.name {
				if !d.inFile {
					return nil, fmt.Errorf("%s:%d: %s is only read from the environment", file, n, key)
				}
				known = true
			}
		}
		if !known {
			return nil, fmt.Errorf("%s:%d: unknown setting %s", file, n, key)
		}
		short := strings.TrimPrefix(key, envPrefix)
		if _, dup := values[short]; dup {
			return nil, fmt.Errorf("%s:%d: %s set twice", file, n, key)
		}
		values[short] = strings.TrimSpace(value)
	}
	return values, sc.Err()
}

func printSettings(s settings, buildx string, buildxErr error) {
	fmt.Println(name, version)
	file := s.file
	if file == "" {
		file = "none (no config directory)"
	} else if _, err := os.Stat(file); errors.Is(err, os.ErrNotExist) {
		file += " (not there)"
	}
	fmt.Println("Config file:", file)
	for _, d := range settingDefs {
		v := s.values[d.name]
		fmt.Printf("%s%s=%s (%s)\n", envPrefix, d.name, v.value, v.source)
	}
	if buildxErr != nil {
		fmt.Println("Real docker-buildx:", buildxErr)
	} else {
		fmt.Println("Real docker-buildx:", buildx)
	}
	if dir, err := os.Getwd(); err == nil {
		if c := inspect(dir, s); c.repo != "" {
			fmt.Printf("Here: %s, branch %q, default branch %q\n", c.repo, c.branch, c.defaultBranch)
		} else {
			fmt.Println("Here: not a git checkout with an origin remote (no cache)")
		}
	}
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

type target struct {
	Context   string            `json:"context"`
	Labels    map[string]string `json:"labels"`
	Platforms []string          `json:"platforms"`
	Output    []any             `json:"output"`
}

// defaultSets returns the --set arguments that add the settings to the
// targets of the bake definition that come from a Compose service
func defaultSets(def []byte, s settings) []string {
	var d struct {
		Target map[string]target `json:"target"`
	}
	if err := json.Unmarshal(def, &d); err != nil {
		// Not the JSON Compose writes (its format changed?): build without
		// the defaults rather than not at all
		warn("not a bake definition from Compose, building without the defaults: %v", err)
		return nil
	}
	names := make([]string, 0, len(d.Target))
	for name, t := range d.Target {
		if t.Labels["com.docker.compose.service"] != "" {
			names = append(names, name)
		}
	}
	sort.Strings(names)
	var sets []string
	if s.get("COMPRESSION") == "zstd" {
		for _, name := range names {
			sets = append(sets, outputSets(name, d.Target[name].Output)...)
		}
	}
	return append(sets, cacheSets(names, d.Target, s)...)
}

// outputSets compresses with zstd the outputs that load the image into the
// local image store, as Compose's are: type=docker, or type=image without
// push. Pushed images are left alone: who pulls them may not read zstd.
func outputSets(name string, outputs []any) []string {
	values := make([]string, len(outputs))
	changed := false
	for i, o := range outputs {
		s, ok := o.(string)
		if !ok {
			warn("%s: an output that isn't a string, left as it is: %v", name, o)
			return nil
		}
		values[i] = s
		if loadsLocally(s) {
			values[i] += ",compression=zstd"
			changed = true
		}
	}
	if !changed {
		return nil
	}
	// The first replaces the target's outputs, the others add to them
	sets := []string{"--set", name + ".output=" + values[0]}
	for _, v := range values[1:] {
		sets = append(sets, "--set", name+".output+="+v)
	}
	return sets
}

func loadsLocally(output string) bool {
	attrs := map[string]string{}
	for _, f := range strings.Split(output, ",") {
		k, v, _ := strings.Cut(f, "=")
		attrs[strings.TrimSpace(k)] = strings.TrimSpace(v)
	}
	if _, set := attrs["compression"]; set {
		return false // the project's own choice
	}
	push, _ := strconv.ParseBool(attrs["push"])
	return attrs["type"] == "docker" || (attrs["type"] == "image" && !push)
}

// cacheSets returns the --set arguments that add the cache to the targets
func cacheSets(names []string, targets map[string]target, s settings) []string {
	from, to := locations(s)
	if len(names) == 0 || (len(from) == 0 && to == "") {
		return nil
	}
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
	// Without force-compression: only the layers that aren't compressed yet
	// (those built here) are, the others are uploaded as they are
	toAttrs := ",mode=max,ignore-error=true,image-manifest=true,oci-mediatypes=true"
	if s.get("COMPRESSION") == "zstd" {
		toAttrs += ",compression=zstd"
	}
	var sets []string
	repos := map[string]checkout{}
	for _, name := range names {
		t := targets[name]
		c, ok := repos[t.Context]
		if !ok {
			c = inspect(t.Context, s)
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
			sets = append(sets, "--set", name+".cache-to+=type=registry,ref="+ref(to, c.branch)+toAttrs)
		}
	}
	return sets
}

// locations are where the cache is imported from (CACHE_FROM,
// comma-separated, in order) and exported to (CACHE_TO, none if empty): each
// a registry with an optional path, host[:port][/path], e.g.
// registry.example/dev. Who reads and writes where is up to the registry's
// access control (see README.md).
func locations(s settings) (from []string, to string) {
	for _, l := range strings.Split(s.get("CACHE_FROM"), ",") {
		if l = location(l); l != "" {
			from = append(from, l)
		}
	}
	return from, location(s.get("CACHE_TO"))
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

func inspect(dir string, s settings) checkout {
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
	c.branch = branch(dir, c.defaultBranch, s.get("BRANCH"))
	return c
}

// branch is the branch being built, "" if unknown. CI checks out a commit
// (detached HEAD): TeamCity's builds name the branch in the BRANCH setting
// (see README.md), GitHub's jobs in their own variables.
func branch(dir, defaultBranch, setting string) string {
	b := setting
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

// findBuildx finds the real docker-buildx: the BUILDX setting, the one an
// install script moved aside, or the first one where the docker CLI looks for
// plugins, after the user's directory (where the wrapper is)
func findBuildx(s settings) (string, error) {
	exe := "docker-buildx"
	if runtime.GOOS == "windows" {
		exe += ".exe"
	}
	if p := s.get("BUILDX"); p != "" {
		if _, err := os.Stat(p); err != nil {
			return "", fmt.Errorf("%sBUILDX (%s): %w", envPrefix, s.values["BUILDX"].source, err)
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
		p := filepath.Join(dir, exe)
		resolved, err := filepath.EvalSymlinks(p)
		if err != nil {
			continue // not there
		}
		if resolved != self {
			return p, nil
		}
	}
	return "", fmt.Errorf("no docker-buildx found besides this wrapper (in %s); set %sBUILDX to its path", strings.Join(dirs, ", "), envPrefix)
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
