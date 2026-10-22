# Changelog

What changed in each release; the settings are described in
[README.md](README.md#settings).

## Unreleased

## v0.4.1 (2026-10-22)

### Fixed

- In a GitHub Actions pull request, the fallback branch is the one it merges
  into (`GITHUB_BASE_REF`), before `origin/HEAD` and the repository's default
  from the job's event, instead of the repository's default;
  `DOCKER_BUILDX_DEFAULTS_FALLBACK_BRANCH` still comes first.
- No fallback branch when nothing names it, instead of `main`, which may not
  be a branch of the repository: builds import only their branch's cache. Set
  `DOCKER_BUILDX_DEFAULTS_FALLBACK_BRANCH` where neither `origin/HEAD` nor
  GitHub names it (e.g. `%vcsroot.branch%` in TeamCity).

## v0.4.0 (2026-10-22)

### Added

- The fallback branch, whose cache a branch's builds import after their own,
  for CI's checkouts, which don't record `origin/HEAD`: until now it was the
  default branch, `main` there, and repositories whose default is another
  branch (e.g. `master`) got no cache from it. It's only imported.
  - `DOCKER_BUILDX_DEFAULTS_FALLBACK_BRANCH` (environment only) names it, e.g.
    `%vcsroot.branch%` in TeamCity; `refs/heads/` is dropped. It comes before
    `origin/HEAD`.
  - In GitHub Actions, without the setting or `origin/HEAD`, it's the
    repository's default, from the job's event (`GITHUB_EVENT_PATH`), when
    the checkout is of the workflow's repository.

### Changed

- TeamCity's `<default>` branch (the VCS root's branch specification doesn't
  match the default branch) is the one `origin/HEAD` points to; without it
  (TeamCity's checkouts don't record it), the build exports nothing, with a
  warning, instead of exporting to `main`, which may be another branch. A
  branch specification like `+:refs/heads/(*)` names the default branch.

## v0.3.0 (2026-10-19)

### Added

- Compression of pushed images: `DOCKER_BUILDX_DEFAULTS_PUSH_COMPRESSION`
  (`gzip`, the default, or `zstd`) adds `compression=zstd` to the outputs
  that push (`type=registry`, or `type=image` with push, as `--push` makes
  them), of every platform.
- Forcing the compression: `DOCKER_BUILDX_DEFAULTS_FORCE_COMPRESSION` (local
  images and the cache) and `DOCKER_BUILDX_DEFAULTS_PUSH_FORCE_COMPRESSION`
  (pushed images), `true`, `false` or `omit` (the default, which passes
  nothing). `true` recompresses the layers that are already compressed,
  base images too.
- Both compressions apply to every `docker buildx bake`, not only Compose's:
  for a bake of the project's own files (e.g. a CI job that pushes images),
  the wrapper resolves the definition with `bake --print` and the same
  arguments, then adds its settings. Bakes that only print, list or check
  pass through. The cache remains Compose's only, and `docker buildx build`
  is still passed through unchanged.

## v0.2.0 (2026-10-18)

The project is now docker-buildx-defaults (it was docker-buildx-cache): it
gives Compose's builds the defaults Compose has no global setting for, not
only the cache.

### Added

- zstd compression, by default: `DOCKER_BUILDX_DEFAULTS_COMPRESSION` (`zstd`
  or `gzip`) adds `compression=zstd` to the images that Compose loads into
  the local image store (`type=docker`, or `type=image` without push) and to
  the cache exports, without forcing it.
- A config file per user (`~/.config/docker-buildx-defaults/config` on
  Linux), `KEY=VALUE` lines with the settings' names: the environment comes
  first (even set to an empty value, which means the default), then the
  file, then the defaults. Unknown settings are errors.
- `docker buildx docker-buildx-defaults` prints each setting and where it
  comes from, the real `docker-buildx`, and the current checkout's
  repository and branches.

### Changed

- The settings are `DOCKER_BUILDX_DEFAULTS_*`: `DOCKER_BUILDX_CACHE_FROM`
  and `_TO` are `DOCKER_BUILDX_DEFAULTS_CACHE_FROM` and `_CACHE_TO`,
  `DOCKER_BUILDX_CACHE_BRANCH` and `_BUILDX` are
  `DOCKER_BUILDX_DEFAULTS_BRANCH` and `_BUILDX`.
- The release binaries are `docker-buildx-defaults-<os>-<arch>`, the
  install scripts move a real `docker-buildx` to
  `~/.docker/docker-buildx-defaults/`, and they recognize the wrapper by
  `docker-buildx-defaults-version`.
- Invalid settings fail every `docker buildx` command with the reason (the
  docker CLI's metadata call still passes through, as the CLI would
  otherwise report a missing plugin).

### Upgrading from v0.1.0

Remove v0.1.0 first, with its `uninstall.sh` (or by deleting the wrapper
from `~/.docker/cli-plugins`): the new install scripts don't recognize it,
and would move it aside as the real `docker-buildx`. Rename the settings,
or move them to the config file.

## v0.1.0 (2026-10-18)

The first release, as docker-buildx-cache: a `docker-buildx` that adds a
layer cache shared through a registry to Compose's builds.

- `cache-from` the current branch, then the default branch, in each
  location of `DOCKER_BUILDX_CACHE_FROM`; `cache-to` the current branch in
  `DOCKER_BUILDX_CACHE_TO`, with `mode=max` and `ignore-error=true`.
- A ref per repository, service, platform and branch.
- The branch from the checkout, GitHub Actions' variables, or
  `DOCKER_BUILDX_CACHE_BRANCH` (e.g. TeamCity's).
- Registries that can't be reached are left out, with a warning.
- Install scripts for Linux, macOS and Windows, checked against the
  release's `SHA256SUMS`; releases with build provenance attestations.
