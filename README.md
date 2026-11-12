# docker-buildx-defaults

Defaults for bake builds (Docker Compose's among them) that buildx and
Compose have no global setting for, with no change to the projects:

- zstd compression of the images and of the cache, instead of BuildKit's
  gzip: faster builds, smaller uploads, and faster pulls where the images
  run (see [Compression](docs/compression.md)).
- a layer cache shared through a registry, for Compose's builds: after a
  one-time setup, `docker compose up --build` (and `docker compose build`)
  reuse the layers that anyone already built, on any branch of the same
  repository, developers and CI alike. Builds still run where they ran; only
  the layers are shared (see [Cache](docs/cache.md)).

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

## Install

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
buildx's version. From then on, Compose's images are compressed with zstd.

`./uninstall.sh` removes the wrapper and puts back what `install.sh` moved.

Each release also has build provenance attestations:
`gh attestation verify <binary> --repo geckosoft-labs/docker-buildx-defaults`.

Then, for the cache:

- [Developer machines](docs/dev-machines.md)
- [CI](docs/ci.md): [GitHub Actions](docs/github-actions.md),
  [TeamCity](docs/teamcity.md), others

## Settings

Each setting comes from the environment, when the variable is set (even to
an empty value), else from the user's config file, else its default; an
empty value is the default.

| Setting | Default | |
|---|---|---|
| `DOCKER_BUILDX_DEFAULTS_COMPRESSION` | `zstd` | Of the images loaded locally, and of the cache: `zstd` or `gzip` (see [Compression](docs/compression.md)) |
| `DOCKER_BUILDX_DEFAULTS_FORCE_COMPRESSION` | `omit` | Recompress the layers already compressed otherwise, for the images loaded locally and the cache: `true`, `false` or `omit` |
| `DOCKER_BUILDX_DEFAULTS_PUSH_COMPRESSION` | `gzip` | Of the images pushed: `zstd` or `gzip` |
| `DOCKER_BUILDX_DEFAULTS_PUSH_FORCE_COMPRESSION` | `omit` | The same, for the images pushed |
| `DOCKER_BUILDX_DEFAULTS_CACHE_FROM` | none | The locations to import the cache from, comma-separated, in order (see [Cache](docs/cache.md#locations)) |
| `DOCKER_BUILDX_DEFAULTS_CACHE_TO` | none | The location to export the cache to |
| `DOCKER_BUILDX_DEFAULTS_CACHE_PREFIX` | the `origin` remote's `<org>/<repo>` | The refs' path after the location, for CI that checks out without `.git`, or to name the project otherwise (see [Cache](docs/cache.md#refs)); environment only |
| `DOCKER_BUILDX_DEFAULTS_BRANCH` | the checked-out branch, or CI's variables | The branch, for CI that doesn't check out one (see [Branches](docs/cache.md#branches)); environment only |
| `DOCKER_BUILDX_DEFAULTS_FALLBACK_BRANCH` | GitHub's pull request's base, else the default branch: `origin/HEAD`, else GitHub's event; none if nothing names it | The branch whose cache is imported after the branch's, never exported to (see [Branches](docs/cache.md#branches)); environment only |
| `DOCKER_BUILDX_DEFAULTS_BUILDX` | found by the wrapper | The real `docker-buildx`, if the wrapper doesn't find it |

The config file has a `KEY=VALUE` line per setting, with the same names;
blank lines and lines starting with `#` are skipped, and unknown settings are
errors. It's in the user's config directory:

- Linux: `$XDG_CONFIG_HOME/docker-buildx-defaults/config`, by default
  `~/.config/docker-buildx-defaults/config`;
- macOS: `~/Library/Application Support/docker-buildx-defaults/config`;
- Windows: `%AppData%\docker-buildx-defaults\config`.

`docker buildx docker-buildx-defaults` prints each setting, where it comes
from, the real `docker-buildx`, and what the cache sees of the current
directory's checkout:

```
docker-buildx-defaults v0.5.0
Config file: /home/me/.config/docker-buildx-defaults/config
DOCKER_BUILDX_DEFAULTS_COMPRESSION=zstd (default)
DOCKER_BUILDX_DEFAULTS_FORCE_COMPRESSION=omit (default)
DOCKER_BUILDX_DEFAULTS_PUSH_COMPRESSION=gzip (default)
DOCKER_BUILDX_DEFAULTS_PUSH_FORCE_COMPRESSION=omit (default)
DOCKER_BUILDX_DEFAULTS_CACHE_FROM=registry.example/dev,registry.example/ci (/home/me/.config/docker-buildx-defaults/config)
DOCKER_BUILDX_DEFAULTS_CACHE_TO=registry.example/dev (/home/me/.config/docker-buildx-defaults/config)
DOCKER_BUILDX_DEFAULTS_CACHE_PREFIX= (default)
DOCKER_BUILDX_DEFAULTS_BRANCH= (default)
DOCKER_BUILDX_DEFAULTS_FALLBACK_BRANCH= (default)
DOCKER_BUILDX_DEFAULTS_BUILDX= (default)
Real docker-buildx: /usr/libexec/docker/cli-plugins/docker-buildx
Here: acme/shop (origin remote), branch "feature/x", fallback branch "main"
```

## Caveats

- A cache location is as trustworthy as whoever can write it: e.g. a CI-only
  one as the jobs that can push there with the CI user's login.
- Only bakes get the defaults, not `docker buildx build` (e.g.
  `docker/build-push-action`); and only Compose's get the cache.
- A bake of the project's files resolves its definition twice (once for
  `bake --print`): a remote definition (a git URL) is fetched twice.
- Secrets passed with `ARG`, or copied in and deleted in a later step, end up
  in the shared cache; `RUN --mount=type=secret` doesn't.
- The wrapper relies on how Compose calls buildx, which isn't documented
  (tested with Compose 5.6.0 and buildx 0.38.0). If that changes, the wrapper
  finds nothing to add and builds go on without its defaults.
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
