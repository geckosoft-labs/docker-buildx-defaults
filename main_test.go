package main

import (
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"
)

func TestRepoPath(t *testing.T) {
	for url, want := range map[string]string{
		"git@github.com:Org/Repo.git":         "org/repo",
		"https://github.com/org/repo":         "org/repo",
		"https://github.com/org/repo.git/":    "org/repo",
		"ssh://git@gitlab.example:2222/a/b/c": "b/c",
		"https://example.com/org/my_repo.git": "org/my_repo",
		"repo":                                "",
	} {
		if got := repoPath(url); got != want {
			t.Errorf("repoPath(%q) = %q, want %q", url, got, want)
		}
	}
}

func TestTag(t *testing.T) {
	for branch, want := range map[string]string{
		"main":                   "main",
		"feature/foo":            "feature-foo",
		"-x":                     "_-x",
		".x":                     "_.x",
		strings.Repeat("a", 200): strings.Repeat("a", 128),
	} {
		if got := tag(branch); got != want {
			t.Errorf("tag(%q) = %q, want %q", branch, got, want)
		}
	}
}

func TestPlatformPath(t *testing.T) {
	t.Setenv("DOCKER_DEFAULT_PLATFORM", "")
	for in, want := range map[string]string{
		"linux/arm64/v8":          "linux-arm64",
		"linux/arm64":             "linux-arm64",
		"linux/arm/v7":            "linux-arm-v7",
		"linux/arm64,linux/amd64": "linux-amd64_linux-arm64",
	} {
		if got := platformPath(strings.Split(in, ",")); got != want {
			t.Errorf("platformPath(%q) = %q, want %q", in, got, want)
		}
	}
	if got, want := platformPath(nil), "linux-"+runtime.GOARCH; got != want {
		t.Errorf("native platform: %q, want %q", got, want)
	}
	t.Setenv("DOCKER_DEFAULT_PLATFORM", "linux/arm64/v8")
	if got := platformPath(nil); got != "linux-arm64" {
		t.Errorf("DOCKER_DEFAULT_PLATFORM: %q", got)
	}
}

func TestReadsStdin(t *testing.T) {
	for args, want := range map[string]bool{
		"--file -":            true,
		"-f -":                true,
		"--file=-":            true,
		"--file x.hcl":        false,
		"--progress plain -f": false,
		"":                    false,
	} {
		if got := readsStdin(strings.Fields(args)); got != want {
			t.Errorf("readsStdin(%q) = %v, want %v", args, got, want)
		}
	}
}

// unsetenv unsets an environment variable for the test
func unsetenv(t *testing.T, key string) {
	t.Helper()
	t.Setenv(key, "") // restored after the test
	os.Unsetenv(key)
}

// envSettings sets every setting in the environment (to its default where
// values doesn't name it), with no config file
func envSettings(t *testing.T, values map[string]string) settings {
	t.Helper()
	for _, d := range settingDefs {
		t.Setenv(envPrefix+d.name, values[d.name])
	}
	s, err := loadSettings("")
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func writeConfig(t *testing.T, content string) string {
	t.Helper()
	file := filepath.Join(t.TempDir(), "config")
	if err := os.WriteFile(file, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	return file
}

func TestLoadSettings(t *testing.T) {
	file := writeConfig(t, "\uFEFF# the office's registries\r\n"+
		"DOCKER_BUILDX_DEFAULTS_CACHE_FROM = reg/dev,reg/ci\r\n"+
		"\r\n"+
		"DOCKER_BUILDX_DEFAULTS_CACHE_TO=reg/dev\r\n"+
		"DOCKER_BUILDX_DEFAULTS_COMPRESSION=gzip\r\n"+
		"DOCKER_BUILDX_DEFAULTS_PUSH_COMPRESSION=zstd\r\n")
	for _, d := range settingDefs {
		unsetenv(t, envPrefix+d.name)
	}
	t.Setenv(envPrefix+"CACHE_TO", "other/dev") // the environment comes first
	t.Setenv(envPrefix+"COMPRESSION", "")       // empty: the default
	s, err := loadSettings(file)
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]setting{
		"CACHE_FROM":       {"reg/dev,reg/ci", file},
		"CACHE_TO":         {"other/dev", "environment"},
		"COMPRESSION":      {"zstd", "environment"},
		"PUSH_COMPRESSION": {"zstd", file},
		"BRANCH":           {"", "default"},
		"BUILDX":           {"", "default"},
	}
	if !reflect.DeepEqual(s.values, want) {
		t.Errorf("\n got %v\nwant %v", s.values, want)
	}

	// No config file
	if s, err = loadSettings(filepath.Join(t.TempDir(), "config")); err != nil {
		t.Fatal(err)
	}
	if got := s.values["CACHE_FROM"]; got != (setting{"", "default"}) {
		t.Errorf("no config file: %v", got)
	}
}

func TestLoadSettingsRejects(t *testing.T) {
	for _, d := range settingDefs {
		unsetenv(t, envPrefix+d.name)
	}
	for content, want := range map[string]string{
		"DOCKER_BUILDX_DEFAULTS_COMPRESSION=lz4":       `"lz4", not gzip or zstd`,
		"DOCKER_BUILDX_DEFAULTS_PUSH_COMPRESSION=none": `PUSH_COMPRESSION (`,
		"DOCKER_BUILDX_DEFAULTS_CACHE_FORM=reg":        ":1: unknown setting DOCKER_BUILDX_DEFAULTS_CACHE_FORM",
		"# x\nDOCKER_BUILDX_DEFAULTS_BRANCH=main":      ":2: DOCKER_BUILDX_DEFAULTS_BRANCH is only read from the environment",
		"reg/dev": ":1: not KEY=VALUE",
		"DOCKER_BUILDX_DEFAULTS_CACHE_TO=a\nDOCKER_BUILDX_DEFAULTS_CACHE_TO=b": ":2: DOCKER_BUILDX_DEFAULTS_CACHE_TO set twice",
	} {
		if _, err := loadSettings(writeConfig(t, content)); err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("%q: %v, want %q", content, err, want)
		}
	}
	t.Setenv(envPrefix+"COMPRESSION", "none")
	if _, err := loadSettings(""); err == nil || !strings.Contains(err.Error(), "(environment)") {
		t.Errorf("COMPRESSION=none in the environment: %v", err)
	}
}

// clearCI unsets the variables of CI that branch reads
func clearCI(t *testing.T) {
	for _, v := range []string{"GITHUB_HEAD_REF", "GITHUB_REF_NAME", "GITHUB_REF_TYPE"} {
		t.Setenv(v, "")
	}
}

// checkoutDir makes a git checkout on branch, with an origin remote
func checkoutDir(t *testing.T, branch string) string {
	t.Helper()
	dir := t.TempDir()
	for _, args := range [][]string{
		{"init", "-q", "-b", branch},
		{"remote", "add", "origin", "git@github.com:org/app.git"},
		{"-c", "user.email=t@t", "-c", "user.name=t", "commit", "-q", "--allow-empty", "-m", "init"},
	} {
		if out, err := exec.Command("git", append([]string{"-C", dir}, args...)...).CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	return dir
}

func detach(t *testing.T, dir string) {
	t.Helper()
	if out, err := exec.Command("git", "-C", dir, "checkout", "-q", "--detach").CombinedOutput(); err != nil {
		t.Fatalf("git checkout --detach: %v\n%s", err, out)
	}
}

func TestBranch(t *testing.T) {
	dir := checkoutDir(t, "feature/x")
	clearCI(t)
	if got := branch(dir, "main", ""); got != "feature/x" {
		t.Errorf("checked-out branch: %q", got)
	}
	detach(t, dir)
	for _, c := range []struct {
		setting string
		env     map[string]string
		want    string
	}{
		{"", nil, ""},
		{"", map[string]string{"GITHUB_REF_TYPE": "branch", "GITHUB_REF_NAME": "main"}, "main"},
		{"", map[string]string{"GITHUB_REF_TYPE": "tag", "GITHUB_REF_NAME": "v1.0"}, ""},
		{"", map[string]string{"GITHUB_HEAD_REF": "feature/pr", "GITHUB_REF_TYPE": "branch", "GITHUB_REF_NAME": "12/merge"}, "feature/pr"},
		{"refs/heads/feature/tc", nil, "feature/tc"},
		{"<default>", nil, "main"},
	} {
		clearCI(t)
		for k, v := range c.env {
			t.Setenv(k, v)
		}
		if got := branch(dir, "main", c.setting); got != c.want {
			t.Errorf("%q %v: %q, want %q", c.setting, c.env, got, c.want)
		}
	}
}

// registry listens like a registry would, for reachable
func registry(t *testing.T) string {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { l.Close() })
	go func() {
		for {
			c, err := l.Accept()
			if err != nil {
				return
			}
			c.Close()
		}
	}()
	return l.Addr().String()
}

// definition is a bake definition as Compose writes it, with a target of a
// service (web) and one of the project's own (own)
func definition(dir string) []byte {
	ctx := strings.ReplaceAll(filepath.ToSlash(dir), `"`, `\"`)
	return []byte(`{"target": {
		"web": {"context": "` + ctx + `", "labels": {"com.docker.compose.service": "web"}, "platforms": ["linux/amd64"], "output": ["type=docker"]},
		"own": {"context": "` + ctx + `", "output": ["type=docker"]}
	}}`)
}

// unreachable is a registry address nothing listens on: a listener's, closed
// at once
func unreachable(t *testing.T) string {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := l.Addr().String()
	l.Close()
	return addr
}

func TestDefaultSetsCache(t *testing.T) {
	dir := checkoutDir(t, "feature/x")
	reg, down := registry(t), unreachable(t)
	clearCI(t)
	from := func(location, branch string) string {
		return "web.cache-from+=type=registry,ref=" + location + "/org/app/web/linux-amd64:" + branch
	}
	to := func(location, branch string) string {
		return "web.cache-to+=type=registry,ref=" + location + "/org/app/web/linux-amd64:" + branch +
			",mode=max,ignore-error=true,image-manifest=true,oci-mediatypes=true"
	}
	for _, c := range []struct {
		from, to string
		want     []string
	}{
		{reg + "/dev, " + reg + "/ci/", reg + "/dev", []string{
			"--set", from(reg+"/dev", "feature-x"),
			"--set", from(reg+"/ci", "feature-x"),
			"--set", from(reg+"/dev", "main"),
			"--set", from(reg+"/ci", "main"),
			"--set", to(reg+"/dev", "feature-x"),
		}},
		{reg, reg, []string{
			"--set", from(reg, "feature-x"),
			"--set", from(reg, "main"),
			"--set", to(reg, "feature-x"),
		}},
		// Import only
		{reg + "/ci", "", []string{
			"--set", from(reg+"/ci", "feature-x"),
			"--set", from(reg+"/ci", "main"),
		}},
		// Export only
		{"", reg, []string{
			"--set", to(reg, "feature-x"),
		}},
		// The locations on an unreachable registry are left out
		{down + "/a," + reg + "/b", down + "/a", []string{
			"--set", from(reg+"/b", "feature-x"),
			"--set", from(reg+"/b", "main"),
		}},
	} {
		// gzip: no compression to add, to the image or the cache
		s := envSettings(t, map[string]string{"CACHE_FROM": c.from, "CACHE_TO": c.to, "COMPRESSION": "gzip"})
		if got := defaultSets(definition(dir), s); !reflect.DeepEqual(got, c.want) {
			t.Errorf("from %q to %q:\n got %q\nwant %q", c.from, c.to, got, c.want)
		}
	}

	s := envSettings(t, map[string]string{"CACHE_FROM": reg, "CACHE_TO": reg})
	want := []string{
		"--set", "web.output=type=docker,compression=zstd",
		"--set", from(reg, "feature-x"),
		"--set", from(reg, "main"),
		"--set", to(reg, "feature-x") + ",compression=zstd",
	}
	if got := defaultSets(definition(dir), s); !reflect.DeepEqual(got, want) {
		t.Errorf("zstd:\n got %q\nwant %q", got, want)
	}
}

func TestDefaultSetsCacheAddsNothing(t *testing.T) {
	dir := checkoutDir(t, "main")
	clearCI(t)
	gzip := func(location string) settings {
		return envSettings(t, map[string]string{"CACHE_FROM": location, "CACHE_TO": location, "COMPRESSION": "gzip"})
	}

	if got := defaultSets(definition(dir), gzip("")); got != nil {
		t.Errorf("no locations: %q", got)
	}
	if got := defaultSets(definition(dir), gzip(unreachable(t))); got != nil {
		t.Errorf("unreachable registry: %q", got)
	}
	reg := gzip(registry(t))
	if got := defaultSets([]byte("not json"), reg); got != nil {
		t.Errorf("not a definition: %q", got)
	}
	if got := defaultSets(definition(t.TempDir()), reg); got != nil {
		t.Errorf("not a git checkout: %q", got)
	}
}

func TestOutputSets(t *testing.T) {
	local := envSettings(t, nil) // the defaults: zstd locally, gzip pushed
	both := envSettings(t, map[string]string{"PUSH_COMPRESSION": "zstd"})
	push := envSettings(t, map[string]string{"COMPRESSION": "gzip", "PUSH_COMPRESSION": "zstd"})
	for _, c := range []struct {
		s       settings
		outputs []any
		want    []string
	}{
		{local, []any{"type=docker"}, []string{"--set", "t.output=type=docker,compression=zstd"}},
		{local, []any{"type=image,name=x/a"}, []string{"--set", "t.output=type=image,name=x/a,compression=zstd"}},
		{local, []any{"type=docker", "type=local,dest=out"}, []string{
			"--set", "t.output=type=docker,compression=zstd",
			"--set", "t.output+=type=local,dest=out",
		}},
		// Pushed: only with PUSH_COMPRESSION
		{local, []any{"type=registry"}, nil},
		{local, []any{"type=image,push=true"}, nil},
		{both, []any{"type=registry"}, []string{"--set", "t.output=type=registry,compression=zstd"}},
		{push, []any{"type=image,push=true"}, []string{"--set", "t.output=type=image,push=true,compression=zstd"}},
		{push, []any{"type=docker"}, nil},
		// As bake --print has them, quoted where needed
		{both, []any{map[string]any{"type": "image", "push": "true", "name": "x/a:1,x/a:latest"}},
			[]string{"--set", `t.output=type=image,"name=x/a:1,x/a:latest",push=true,compression=zstd`}},
		{local, []any{map[string]any{"type": "docker"}}, []string{"--set", "t.output=type=docker,compression=zstd"}},
		// The project's own compression
		{both, []any{"type=docker,compression=gzip"}, nil},
		{both, []any{map[string]any{"type": "registry", "compression": "gzip"}}, nil},
		{both, []any{`"type=image,push=true",compression=gzip`}, nil},
		{both, []any{42}, nil},
		{both, nil, nil},
	} {
		if got := outputSets("t", c.outputs, c.s); !reflect.DeepEqual(got, c.want) {
			t.Errorf("%v %v:\n got %q\nwant %q", c.s.values, c.outputs, got, c.want)
		}
	}
}

func TestBuilds(t *testing.T) {
	for args, want := range map[string]bool{
		"":                       true,
		"--push app":             true,
		"-f compose.yaml --load": true,
		"--print":                false,
		"--list targets":         false,
		"--list=variables":       false,
		"--call check":           false,
		"--check app":            false,
		"--progress plain -h":    false,
	} {
		if got := builds(strings.Fields(args)); got != want {
			t.Errorf("builds(%q) = %v, want %v", args, got, want)
		}
	}
}

// fakeBuildx is a docker-buildx that answers bake --print with def, and
// records its arguments in args.txt next to it
func fakeBuildx(t *testing.T, def string) string {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("the fake buildx is a shell script")
	}
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "def.json"), []byte(def), 0o644); err != nil {
		t.Fatal(err)
	}
	script := "#!/bin/sh\necho \"$@\" > " + dir + "/args.txt\ncat " + dir + "/def.json\n"
	p := filepath.Join(dir, "docker-buildx")
	if err := os.WriteFile(p, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestFileSets(t *testing.T) {
	buildx := fakeBuildx(t, `{"group": {"default": {"targets": ["api", "web"]}}, "target": {
		"web": {"context": ".", "output": [{"type": "image", "push": "true"}]},
		"api": {"context": ".", "output": [{"type": "docker"}]}
	}}`)
	s := envSettings(t, map[string]string{"PUSH_COMPRESSION": "zstd"})
	want := []string{
		"--set", "api.output=type=docker,compression=zstd",
		"--set", "web.output=type=image,push=true,compression=zstd",
	}
	if got := fileSets(buildx, []string{"buildx", "bake", "--push"}, []string{"--push"}, s); !reflect.DeepEqual(got, want) {
		t.Errorf("\n got %q\nwant %q", got, want)
	}
	b, err := os.ReadFile(filepath.Join(filepath.Dir(buildx), "args.txt"))
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.TrimSpace(string(b)); got != "buildx bake --push --print" {
		t.Errorf("bake --print run as %q", got)
	}

	// Nothing to compress: bake --print isn't asked
	gzip := envSettings(t, map[string]string{"COMPRESSION": "gzip"})
	if got := fileSets("/nonexistent", nil, []string{"--push"}, gzip); got != nil {
		t.Errorf("gzip: %q", got)
	}
	// Not a build
	if got := fileSets("/nonexistent", nil, []string{"--print"}, s); got != nil {
		t.Errorf("--print: %q", got)
	}
	// bake --print fails: building without the defaults
	if got := fileSets(filepath.Join(t.TempDir(), "missing"), nil, nil, s); got != nil {
		t.Errorf("failing bake --print: %q", got)
	}
}

func TestDefaultSetsCompressionOnly(t *testing.T) {
	// Compression applies with no cache, and outside a git checkout
	s := envSettings(t, nil)
	want := []string{"--set", "web.output=type=docker,compression=zstd"}
	if got := defaultSets(definition(t.TempDir()), s); !reflect.DeepEqual(got, want) {
		t.Errorf("\n got %q\nwant %q", got, want)
	}
}
