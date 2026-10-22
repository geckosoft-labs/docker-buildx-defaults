# docker-buildx-defaults

Defaults for bake builds (Docker Compose's among them) that buildx and
Compose have no global setting for, with no change to the projects:

- zstd compression of the images and of the cache, instead of BuildKit's
  gzip: faster builds, smaller uploads, and faster pulls where the images
  run.
- a layer cache shared through a registry, for Compose's builds: after a
  one-time setup, `docker compose up --build` (and `docker compose build`)
  reuse the layers that anyone already built, on any branch of the same
  repository, developers and CI alike. Builds still run where they ran; only
  the layers are shared.

## How it works

docker-buildx-defaults is a `docker-buildx` that wraps the real one,
installed where the docker CLI and Compose look for plugins before the
system ones. It adds its settings to bake's targets, with `--set`:

- Docker Compose builds by running `docker-buildx bake --file -`, with the
  build definition on stdin: the wrapper reads it, and adds its settings to
  each target that comes from a Compose service.
- Any other `docker buildx bake` (of the project's bake or Compose files,
  e.g. a CI job that pushes images): the wrapper first asks the real one for
  the definition it resolves from the same arguments (`bake --print`), and
  adds the compression to its targets.

Everything else (`docker buildx build`, the other commands, and bakes that
only print, list or check) passes through unchanged. Compose doesn't show the
wrapper's warnings;
`docker buildx docker-buildx-defaults` shows the settings in effect, and where
each comes from (see [Settings](#settings)).

### Compression

BuildKit compresses layers with gzip, and has no setting for another default.
The wrapper adds `compression=zstd`:

- with `DOCKER_BUILDX_DEFAULTS_COMPRESSION=zstd` (the default), to the image
  outputs that load into the local image store, which is where Compose's
  builds go (`type=docker`, or `type=image` without push), and to the exports
  of the [cache](#cache), when there's one;
- with `DOCKER_BUILDX_DEFAULTS_PUSH_COMPRESSION=zstd`, to the outputs that
  push (`type=registry`, or `type=image` with push, as `--push` makes them),
  of every platform. It's off by default (`gzip`): whoever pulls the images
  must read zstd, e.g. Docker since 23.0, containerd since 1.5, Kubernetes
  nodes with those, Amazon ECS on Fargate. Turn it on where you know who
  pulls them, e.g. in CI for images that are deployed.

A project's own `compression=` is left alone.

Layers that are already compressed (pulled base images, layers imported from
the cache, layers the builder compressed with gzip for an earlier build) are
kept as they are; only the ones compressed for this build use zstd.
`DOCKER_BUILDX_DEFAULTS_FORCE_COMPRESSION=true` (local images and the cache)
and `DOCKER_BUILDX_DEFAULTS_PUSH_FORCE_COMPRESSION=true` (pushed images)
recompress them all (`force-compression=true`), base images too: every
build then pays for it, which is worth it for images pulled more often than
they're built, e.g. those deployed from CI. `false` passes
`force-compression=false`; `omit`, the default, passes nothing. Exporting
the images is the slowest step of many builds once the cache has the rest:
in a stack with a 5 GB image, zstd cut it from 100 s to 45 s, and its layers
came out 4% smaller.

zstd layers come with OCI media types, which BuildKit then uses for the
image. `gzip` (with forcing omitted) leaves the outputs, and the cache, to
BuildKit.

### Cache

For each service that is built from a git checkout, the wrapper adds:

- `cache-from` the current branch, then the fallback branch (by default the
  branch a pull request merges into, else the repository's default branch),
  for a branch's first builds, in each location of
  `DOCKER_BUILDX_DEFAULTS_CACHE_FROM`;
- `cache-to` the current branch, in the location
  `DOCKER_BUILDX_DEFAULTS_CACHE_TO`, with `mode=max` (intermediate stages too)
  and `ignore-error=true` (a failed upload doesn't fail the build).

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

Services built without a git remote get no cache, and neither does anything
when no location is set. The locations on a registry that's unreachable are
left out, and the build goes on without them.

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

1. Install the wrapper, from the latest release (or the one given):

   - Linux and macOS:

     ```sh
     curl -fsSL https://raw.githubusercontent.com/geckosoft-labs/docker-buildx-defaults/main/install.sh | bash
     # or, from a clone: ./install.sh [v0.2.0]
     ```

   - Windows (PowerShell):

     ```powershell
     irm https://raw.githubusercontent.com/geckosoft-labs/docker-buildx-defaults/main/install.ps1 | iex
     # or, from a clone: .\install.ps1 [-Version v0.2.0]
     ```

   The binary is checked against the release's `SHA256SUMS`. A real
   `docker-buildx` found in the user's plugin directory moves aside (to
   `~/.docker/docker-buildx-defaults/`), where the wrapper finds it. The
   scripts end with `docker buildx version`, which must print the real
   buildx's version.

   From then on, Compose's images are compressed with zstd.

2. For the cache, the containerd image store, which the classic one can't
   export a cache from: Docker Desktop → Settings → General → "Use
   containerd for pulling and storing images"; Docker Engine: `"features":
   {"containerd-snapshotter": true}` in `/etc/docker/daemon.json`, then
   restart it. Images in the old store aren't seen after the switch: the
   first builds are cold.

3. The cache's locations, in the config file (see [Settings](#settings)),
   e.g. `~/.config/docker-buildx-defaults/config` on Linux:

   ```sh
   DOCKER_BUILDX_DEFAULTS_CACHE_FROM=registry.example/dev,registry.example/ci
   DOCKER_BUILDX_DEFAULTS_CACHE_TO=registry.example/dev
   ```

   then `docker login` to their registries.

`./uninstall.sh` removes the wrapper and puts back what `install.sh` moved.

Each release also has build provenance attestations:
`gh attestation verify <binary> --repo geckosoft-labs/docker-buildx-defaults`.

## CI

On runners and build agents, install the release binary as
`/usr/local/lib/docker/cli-plugins/docker-buildx` (Linux), which the docker
CLI searches before the package's `/usr/libexec/docker/cli-plugins`. In the
config file of the user the jobs run as (or in their environment), set
`DOCKER_BUILDX_DEFAULTS_PUSH_COMPRESSION=zstd` (and
`DOCKER_BUILDX_DEFAULTS_PUSH_FORCE_COMPRESSION=true`) if what pulls the
images reads zstd, and the cache's `DOCKER_BUILDX_DEFAULTS_CACHE_FROM` and
`DOCKER_BUILDX_DEFAULTS_CACHE_TO`, with the docker login of the registry's
CI user.

CI checks out a commit, not a branch, and doesn't record which branch is the
repository's default (`origin/HEAD`), so the wrapper finds both elsewhere.
The branch is where the cache is exported, so it's never guessed: a build of
an unknown branch exports nothing. The fallback branch is only imported.

- GitHub Actions: `GITHUB_HEAD_REF` (the source branch of a pull request), or
  `GITHUB_REF_NAME` when the run is for a branch. A tag's build imports the
  fallback branch and exports nothing. The fallback branch is the one a pull
  request merges into (`GITHUB_BASE_REF`), else the repository's default,
  from the job's event (`GITHUB_EVENT_PATH`), when the checkout is of the
  workflow's repository.
- TeamCity: set the parameter `env.DOCKER_BUILDX_DEFAULTS_BRANCH` to
  `%teamcity.build.branch%`, the build's logical branch name (e.g. in the
  root project, for every build); `refs/heads/` is dropped. The VCS root's
  branch specification must match the default branch too, with logical names
  that are the branches' own, e.g. `+:refs/heads/(*)`: otherwise its builds
  get `<default>`, which is the branch `origin/HEAD` points to, and TeamCity's
  checkouts don't record it, so they export nothing, with a warning. In the
  build configurations (or templates) with one VCS root, set
  `env.DOCKER_BUILDX_DEFAULTS_FALLBACK_BRANCH` to `%vcsroot.branch%`, the VCS
  root's default branch (in the others, the reference doesn't resolve, and
  TeamCity doesn't start their builds); without it, there's no fallback
  branch. TeamCity's server-side checkout leaves no `.git` on the agent:
  builds checked out so don't use the cache.
- Elsewhere: `DOCKER_BUILDX_DEFAULTS_BRANCH` and
  `DOCKER_BUILDX_DEFAULTS_FALLBACK_BRANCH`.

## Registry

Any registry that accepts BuildKit's cache manifests (OCI image manifests
with a `application/vnd.buildkit.cacheconfig.v0` config). It needs:

- read access to the locations of `DOCKER_BUILDX_DEFAULTS_CACHE_FROM`, and
  write access to the one of `DOCKER_BUILDX_DEFAULTS_CACHE_TO`, for the users
  of each: e.g. developers write `dev/**`, and only CI writes `ci/**`;
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

A registry's UI shows the caches as images with no platform, creation time
or labels: their config is BuildKit's cache graph, not an image's. The
platform is in the ref.

## Settings

Each setting comes from the environment, when the variable is set (even to
an empty value), else from the user's config file, else its default; an
empty value is the default.

| Setting | Default | |
|---|---|---|
| `DOCKER_BUILDX_DEFAULTS_COMPRESSION` | `zstd` | Of the images loaded locally, and of the cache: `zstd` or `gzip` (see [Compression](#compression)) |
| `DOCKER_BUILDX_DEFAULTS_FORCE_COMPRESSION` | `omit` | Recompress the layers already compressed otherwise, for the images loaded locally and the cache: `true`, `false` or `omit` |
| `DOCKER_BUILDX_DEFAULTS_PUSH_COMPRESSION` | `gzip` | Of the images pushed: `zstd` or `gzip` |
| `DOCKER_BUILDX_DEFAULTS_PUSH_FORCE_COMPRESSION` | `omit` | The same, for the images pushed |
| `DOCKER_BUILDX_DEFAULTS_CACHE_FROM` | none | The locations to import the cache from, comma-separated, in order (see [Cache](#cache)) |
| `DOCKER_BUILDX_DEFAULTS_CACHE_TO` | none | The location to export the cache to |
| `DOCKER_BUILDX_DEFAULTS_BRANCH` | the checked-out branch, or CI's variables | The branch, for CI that doesn't name it itself (see [CI](#ci)); environment only |
| `DOCKER_BUILDX_DEFAULTS_FALLBACK_BRANCH` | GitHub's pull request's base, else the default branch: `origin/HEAD`, else GitHub's event; none if nothing names it | The branch whose cache is imported after the branch's, never exported to (see [CI](#ci)); environment only |
| `DOCKER_BUILDX_DEFAULTS_BUILDX` | found by the wrapper | The real `docker-buildx`, if the wrapper doesn't find it |

The config file has a `KEY=VALUE` line per setting, with the same names;
blank lines and lines starting with `#` are skipped, and unknown settings are
errors. It's in the user's config directory:

- Linux: `$XDG_CONFIG_HOME/docker-buildx-defaults/config`, by default
  `~/.config/docker-buildx-defaults/config`;
- macOS: `~/Library/Application Support/docker-buildx-defaults/config`;
- Windows: `%AppData%\docker-buildx-defaults\config`.

Unlike a shell profile, it reaches every `docker compose`, also those
started by an IDE or another app.

`docker buildx docker-buildx-defaults` prints each setting, where it comes
from, the real `docker-buildx`, and what the cache sees of the current
directory's checkout:

```
docker-buildx-defaults v0.2.0
Config file: /home/me/.config/docker-buildx-defaults/config
DOCKER_BUILDX_DEFAULTS_COMPRESSION=zstd (default)
DOCKER_BUILDX_DEFAULTS_FORCE_COMPRESSION=omit (default)
DOCKER_BUILDX_DEFAULTS_PUSH_COMPRESSION=gzip (default)
DOCKER_BUILDX_DEFAULTS_PUSH_FORCE_COMPRESSION=omit (default)
DOCKER_BUILDX_DEFAULTS_CACHE_FROM=registry.example/dev,registry.example/ci (/home/me/.config/docker-buildx-defaults/config)
DOCKER_BUILDX_DEFAULTS_CACHE_TO=registry.example/dev (/home/me/.config/docker-buildx-defaults/config)
DOCKER_BUILDX_DEFAULTS_BRANCH= (default)
DOCKER_BUILDX_DEFAULTS_FALLBACK_BRANCH= (default)
DOCKER_BUILDX_DEFAULTS_BUILDX= (default)
Real docker-buildx: /usr/libexec/docker/cli-plugins/docker-buildx
Here: acme/shop, branch "feature/x", fallback branch "main"
```

The fallback branch is `DOCKER_BUILDX_DEFAULTS_FALLBACK_BRANCH`, else, in a
GitHub Actions pull request, the branch it merges into, else the
repository's default branch: the one `origin/HEAD` points to, else, in GitHub
Actions, the repository's (from the job's event). When nothing names it, as
in CI's checkouts elsewhere, which don't record `origin/HEAD`
(`git remote set-head origin --auto` records it), builds import only their
branch's cache.

## Caveats

- A location is as trustworthy as whoever can write it: e.g. a CI-only one
  as the jobs that can push there with the CI user's login.
- Only bakes get the defaults, not `docker buildx build` (e.g.
  `docker/build-push-action`); and only Compose's get the cache.
- A bake of the project's files resolves its definition twice (once for
  `bake --print`): a remote definition (a git URL) is fetched twice.
- Secrets passed with `ARG`, or copied in and deleted in a later step, end up
  in the shared cache; `RUN --mount=type=secret` doesn't.
- The wrapper relies on how Compose calls buildx, which isn't documented
  (tested with Compose 5.6.0 and buildx 0.38.0). If that changes, the wrapper
  finds nothing to add and builds go on without its defaults.
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
- An invalid config file or setting fails every `docker buildx` command,
  with the reason.

## Development

```sh
go vet ./... && go test ./...
./build.sh dev   # every release binary, in dist/
```

A release is a tag: first move the changes under "Unreleased" in
[CHANGELOG.md](CHANGELOG.md) to a section of the new version, then
`git tag v0.3.0 && git push origin v0.3.0` runs
`.github/workflows/release.yml`.
