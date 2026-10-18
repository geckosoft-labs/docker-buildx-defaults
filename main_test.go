package main

import (
	"net"
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

// clearCI unsets the variables of CI that branch reads
func clearCI(t *testing.T) {
	for _, v := range []string{"DOCKER_BUILDX_CACHE_BRANCH", "GITHUB_HEAD_REF", "GITHUB_REF_NAME", "GITHUB_REF_TYPE"} {
		t.Setenv(v, "")
	}
}

// checkout makes a git checkout on branch, with an origin remote
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
	if got := branch(dir, "main"); got != "feature/x" {
		t.Errorf("checked-out branch: %q", got)
	}
	detach(t, dir)
	for _, c := range []struct {
		env  map[string]string
		want string
	}{
		{nil, ""},
		{map[string]string{"GITHUB_REF_TYPE": "branch", "GITHUB_REF_NAME": "main"}, "main"},
		{map[string]string{"GITHUB_REF_TYPE": "tag", "GITHUB_REF_NAME": "v1.0"}, ""},
		{map[string]string{"GITHUB_HEAD_REF": "feature/pr", "GITHUB_REF_TYPE": "branch", "GITHUB_REF_NAME": "12/merge"}, "feature/pr"},
		{map[string]string{"DOCKER_BUILDX_CACHE_BRANCH": "refs/heads/feature/tc"}, "feature/tc"},
		{map[string]string{"DOCKER_BUILDX_CACHE_BRANCH": "<default>"}, "main"},
	} {
		clearCI(t)
		for k, v := range c.env {
			t.Setenv(k, v)
		}
		if got := branch(dir, "main"); got != c.want {
			t.Errorf("%v: %q, want %q", c.env, got, c.want)
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

func definition(dir string) []byte {
	ctx := strings.ReplaceAll(filepath.ToSlash(dir), `"`, `\"`)
	return []byte(`{"target": {
		"web": {"context": "` + ctx + `", "labels": {"com.docker.compose.service": "web"}, "platforms": ["linux/amd64"]},
		"own": {"context": "` + ctx + `"}
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

func TestCacheSets(t *testing.T) {
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
		t.Setenv("DOCKER_BUILDX_CACHE_FROM", c.from)
		t.Setenv("DOCKER_BUILDX_CACHE_TO", c.to)
		if got := cacheSets(definition(dir)); !reflect.DeepEqual(got, c.want) {
			t.Errorf("from %q to %q:\n got %q\nwant %q", c.from, c.to, got, c.want)
		}
	}
}

func TestCacheSetsAddsNothing(t *testing.T) {
	dir := checkoutDir(t, "main")
	clearCI(t)

	t.Setenv("DOCKER_BUILDX_CACHE_FROM", "")
	t.Setenv("DOCKER_BUILDX_CACHE_TO", "")
	if got := cacheSets(definition(dir)); got != nil {
		t.Errorf("no locations: %q", got)
	}

	down := unreachable(t)
	t.Setenv("DOCKER_BUILDX_CACHE_FROM", down)
	t.Setenv("DOCKER_BUILDX_CACHE_TO", down)
	if got := cacheSets(definition(dir)); got != nil {
		t.Errorf("unreachable registry: %q", got)
	}

	reg := registry(t)
	t.Setenv("DOCKER_BUILDX_CACHE_FROM", reg)
	t.Setenv("DOCKER_BUILDX_CACHE_TO", reg)
	if got := cacheSets([]byte("not json")); got != nil {
		t.Errorf("not a definition: %q", got)
	}
	if got := cacheSets(definition(t.TempDir())); got != nil {
		t.Errorf("not a git checkout: %q", got)
	}
}
