# docker-buildx-defaults

Shares the Docker layer cache of Compose builds through a registry, with no
change to the projects: after a one-time setup, `docker compose up --build`
(and `docker compose build`) reuse the layers that anyone already built, on
any branch of the same repository, developers and CI alike. Builds still run
where they ran; only the layers are shared.

## How it works

Docker Compose builds by running `docker-buildx bake --file -`, with the build
definition on stdin. docker-buildx-defaults is a `docker-buildx` that wraps the
real one, installed where the docker CLI and Compose look for plugins before
the system ones. For each service of the definition that is built from a git
checkout, it adds:

- `cache-from` the current branch, then the repository's default branch, in
  each location of `DOCKER_BUILDX_DEFAULTS_CACHE_FROM`;
- `cache-to` the current branch, in the location `DOCKER_BUILDX_DEFAULTS_CACHE_TO`,
  with `mode=max` (intermediate stages too) and `ignore-error=true` (a failed
  upload doesn't fail the build).

A location is a registry with an optional path, `host[:port][/path]`. The
refs are `<location>/<org>/<repo>/<service>/<platform>:<branch>`, from the
`origin` remote: e.g. `registry.example/dev/acme/shop/web/linux-arm64:feature-x`
for the `web` service of `github.com/acme/shop`, built for `linux/arm64` on
branch `feature/x`, in the location `registry.example/dev`.

Who reads and writes which locations is up to you, and to the registry's
access control (see [Registry](#registry)). For example:

| | `DOCKER_BUILDX_DEFAULTS_CACHE_FROM` | `DOCKER_BUILDX_DEFAULTS_CACHE_TO` |
|---|---|---|
| One cache for everyone | `registry.example` | `registry.example` |
| Developers | `registry.example/dev,registry.example/ci` | `registry.example/dev` |
| CI | `registry.example/ci` | `registry.example/ci` |
| Read-only (e.g. a contractor) | `registry.example/ci` | |

In the second and third rows, with a registry where only CI can write `ci/`,
CI builds (releases among them) reuse only layers that CI built; developers
reuse those too, and get the default branch from CI's builds of it.

An export replaces the whole ref, so each platform has its own: machines of
different architectures building the same branch would otherwise keep
replacing each other's cache, which the other can't use. The platform is the
one the service names (`platform:` in Compose), else
`DOCKER_DEFAULT_PLATFORM`, else Linux on the machine's architecture, which is
what Docker builds for by default (Docker Desktop too).

The local build cache still comes first: the registry is only asked for the
steps that aren't cached locally, so rebuilding the same branch is as fast as
before, offline too.

Everything else (`docker buildx ...`, bake files of your own, builds with no
git remote) passes through unchanged, and so does everything when neither
`DOCKER_BUILDX_DEFAULTS_CACHE_FROM` nor `DOCKER_BUILDX_DEFAULTS_CACHE_TO` is set. The
locations on a registry that's unreachable are left out, and the build goes
on without them. Compose doesn't show the wrapper's warnings: the cache is in
use when `docker compose build --progress plain` shows `importing cache
manifest from <registry>/...` steps.

### Reading the build output

Every build starts by importing each ref of `cache-from`, every time, also
when the local cache has everything: BuildKit asks the registry what it has
before it builds. A ref nobody exported yet (a branch nobody built in that
location, e.g. the default branch before CI or anyone builds it) shows as an
error, and the build goes on without it:

```
#65 [web] importing cache manifest from registry.example/dev/acme/shop/web/linux-amd64:main
#65 ERROR: failed to configure registry cache importer: registry.example/dev/acme/shop/web/linux-amd64:main: not found
```

That's expected; the ref appears with the first build that exports it. What
deserves a look:

- `401 Unauthorized` or `403 Forbidden` on import or export: the login
  (`docker login <registry>`), or the registry's access control for that
  location;
- an error in an `exporting cache to registry` step: the export failed (it
  doesn't fail the build, `ignore-error=true`), e.g. the registry is full.

## Setup (once per machine)

1. The containerd image store, which the classic one can't export a cache
   from: Docker Desktop → Settings → General → "Use containerd for pulling and
   storing images"; Docker Engine: `"features": {"containerd-snapshotter":
   true}` in `/etc/docker/daemon.json`, then restart it. Images in the old
   store aren't seen after the switch: the first builds are cold.

2. The locations, in the environment of the shell profile (or wherever
   `docker compose` is started from, e.g. an IDE), e.g.
   `export DOCKER_BUILDX_DEFAULTS_CACHE_FROM=registry.example/dev,registry.example/ci DOCKER_BUILDX_DEFAULTS_CACHE_TO=registry.example/dev`;
   then `docker login` to their registries.

3. Install the wrapper, from the latest release (or the one given):

   - Linux and macOS:

     ```sh
     curl -fsSL https://raw.githubusercontent.com/geckosoft-labs/docker-buildx-defaults/main/install.sh | bash
     # or, from a clone: ./install.sh [v0.1.0]
     ```

   - Windows (PowerShell):

     ```powershell
     irm https://raw.githubusercontent.com/geckosoft-labs/docker-buildx-defaults/main/install.ps1 | iex
     # or, from a clone: .\install.ps1 [-Version v0.1.0]
     ```

   The binary is checked against the release's `SHA256SUMS`. A real
   `docker-buildx` found in the user's plugin directory moves aside (to
   `~/.docker/docker-buildx-defaults/`), where the wrapper finds it. The scripts
   end with `docker buildx version`, which must print the real buildx's
   version.

`./uninstall.sh` removes the wrapper and puts back what `install.sh` moved.

Each release also has build provenance attestations:
`gh attestation verify <binary> --repo geckosoft-labs/docker-buildx-defaults`.

## CI

On runners and build agents, install the release binary as
`/usr/local/lib/docker/cli-plugins/docker-buildx` (Linux), which the docker
CLI searches before the package's `/usr/libexec/docker/cli-plugins`, and give
the jobs `DOCKER_BUILDX_DEFAULTS_CACHE_FROM`, `DOCKER_BUILDX_DEFAULTS_CACHE_TO` and the docker
login of the registry's CI user.

CI checks out a commit, not a branch, so the wrapper finds the branch
elsewhere:

- GitHub Actions: `GITHUB_HEAD_REF` (the source branch of a pull request), or
  `GITHUB_REF_NAME` when the run is for a branch. A tag's build imports the
  default branch and exports nothing.
- TeamCity: set the parameter `env.DOCKER_BUILDX_DEFAULTS_BRANCH` to
  `%teamcity.build.branch%` (e.g. in the root project, for every build):
  `refs/heads/` is dropped, and `<default>` is the default branch.
  TeamCity's server-side checkout leaves no `.git` on the agent: builds
  checked out so don't use the cache.
- Elsewhere: `DOCKER_BUILDX_DEFAULTS_BRANCH`.

## Registry

Any registry that accepts BuildKit's cache manifests (OCI image manifests
with a `application/vnd.buildkit.cacheconfig.v0` config). It needs:

- read access to the locations of `DOCKER_BUILDX_DEFAULTS_CACHE_FROM`, and write
  access to the one of `DOCKER_BUILDX_DEFAULTS_CACHE_TO`, for the users of each: e.g.
  developers write `dev/**`, and only CI writes `ci/**`;
- expiry: every export replaces a tag, leaving the previous manifest
  untagged, and branches stop being built.

With [zot](https://zotregistry.dev), for example, retention deletes the
tags that nobody pulled for a while (BuildKit's cache imports count as pulls,
and pushes start the clock), here after 3 days in the `dev` location of the
example above, and after 7 in the `ci` one:

```json
"storage": {
  "rootDirectory": "/var/lib/registry",
  "retention": {
    "policies": [
      { "repositories": ["dev/**"], "keepTags": [{ "pulledWithin": "72h" }] },
      { "repositories": ["ci/**"], "keepTags": [{ "pulledWithin": "168h" }] }
    ]
  }
}
```

zot's defaults do the rest: the untagged manifests that exports leave behind
are deleted too (`deleteUntagged`), and garbage collection, every hour,
deletes the blobs no manifest uses any more.

zot has no size-based eviction: the retention windows, and a quota on its
storage, keep it in bounds. When the storage is full, exports fail and builds
go on (`ignore-error`).

## Settings

| Environment variable | Default | |
|---|---|---|
| `DOCKER_BUILDX_DEFAULTS_CACHE_FROM` | none | The locations to import from, comma-separated, in order (see [How it works](#how-it-works)) |
| `DOCKER_BUILDX_DEFAULTS_CACHE_TO` | none | The location to export to |
| `DOCKER_BUILDX_DEFAULTS_BRANCH` | the checked-out branch, or CI's variables | The branch, for CI that doesn't name it itself (see [CI](#ci)) |
| `DOCKER_BUILDX_DEFAULTS_BUILDX` | found by the wrapper | The real `docker-buildx`, if the wrapper doesn't find it |

The default branch is the one `origin/HEAD` points to; `main` when the clone
didn't record it (`git remote set-head origin --auto` records it), as in CI's
shallow checkouts.

## Caveats

- A location is as trustworthy as whoever can write it: e.g. a CI-only one
  as the jobs that can push there with the CI user's login.
- Only Compose's builds get the cache: not `docker buildx build` (e.g.
  `docker/build-push-action`), nor bake files of a project's own.
- Secrets passed with `ARG`, or copied in and deleted in a later step, end up
  in the shared cache; `RUN --mount=type=secret` doesn't.
- The wrapper relies on how Compose calls buildx, which isn't documented
  (tested with Compose 5.6.0 and buildx 0.38.0). If that changes, the wrapper
  finds nothing to add and builds go on without the cache.
- Docker Desktop may put its own `docker-buildx` back in
  `~/.docker/cli-plugins` (e.g. on an update): `docker buildx version` then
  stops going through the wrapper; install it again.
- Hits need identical inputs: the same base image and the same files for each
  step. Dockerfiles that copy the dependency manifests and install them before
  `COPY . .` share most.
- The cache is per platform: Apple Silicon machines (arm64) don't reuse
  layers built on amd64 ones, and the other way round. Two machines of the
  same platform building the same branch do replace each other's export,
  which is of use to both.
- The platform is guessed wrong for a daemon of another architecture than
  the machine (a remote `DOCKER_HOST`): that costs cache hits, not
  correctness, as BuildKit's cache keys include the platform; setting
  `DOCKER_DEFAULT_PLATFORM` fixes it.

## Development

```sh
go vet ./... && go test ./...
./build.sh dev   # every release binary, in dist/
```

A release is a tag: `git tag v0.1.0 && git push origin v0.1.0` runs
`.github/workflows/release.yml`.
