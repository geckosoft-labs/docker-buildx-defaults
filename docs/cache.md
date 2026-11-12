# Cache

For each service that is built from a git checkout (or with a cache prefix,
see below), the wrapper adds:

- `cache-from` the current branch, then the [fallback branch](#branches),
  for a branch's first builds, in each location of
  `DOCKER_BUILDX_DEFAULTS_CACHE_FROM`;
- `cache-to` the current branch, in the location
  `DOCKER_BUILDX_DEFAULTS_CACHE_TO`, with `mode=max` (intermediate stages too)
  and `ignore-error=true` (a failed upload doesn't fail the build).

## Refs

A location is a registry with an optional path, `host[:port][/path]`. The
refs are `<location>/<org>/<repo>/<service>/<platform>:<branch>`, from the
`origin` remote: e.g. `registry.example/dev/acme/shop/web/linux-arm64:feature-x`
for the `web` service of `github.com/acme/shop`, built for `linux/arm64` on
branch `feature/x`, in the location `registry.example/dev`.

`DOCKER_BUILDX_DEFAULTS_CACHE_PREFIX` (environment only, as it's the
build's) replaces `<org>/<repo>`, with a checkout or without one (e.g. CI
that checks out without `.git`): `DOCKER_BUILDX_DEFAULTS_CACHE_PREFIX=shop`
gives `registry.example/dev/shop/web/linux-arm64:feature-x`. Each of its
`/`-separated parts is made valid in a registry path, as the remote's are.

An export replaces the whole ref, so each platform has its own: machines of
different architectures building the same branch would otherwise keep
replacing each other's cache, which the other can't use. The platform is the
one the service names (`platform:` in Compose), else
`DOCKER_DEFAULT_PLATFORM`, else Linux on the machine's architecture, which is
what Docker builds for by default (Docker Desktop too).

Services built neither from a git checkout with an `origin` remote nor with
a cache prefix get no cache, with a warning (which Compose doesn't show: see
`docker buildx docker-buildx-defaults`, under
[Settings](../README.md#settings)), and neither does anything when no
location is set. The locations on a registry that's unreachable are left
out, and the build goes on without them.

## Branches

The branch is where the cache is exported, so it's never guessed: a build of
an unknown branch exports nothing. It's `DOCKER_BUILDX_DEFAULTS_BRANCH`, else
the checked-out branch, else, in GitHub Actions, the job's (see
[CI](ci.md)).

The fallback branch is only imported, for a branch's first builds: a wrong
one costs cache hits, nothing else. It's
`DOCKER_BUILDX_DEFAULTS_FALLBACK_BRANCH`, else, in a GitHub Actions pull
request, the branch it merges into, else the repository's default branch:
the one `origin/HEAD` points to, else, in GitHub Actions, the repository's
(from the job's event). When nothing names it, as in most CI checkouts,
which don't record `origin/HEAD` (`git remote set-head origin --auto` records
it), builds import only their branch's cache.

## Locations

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

The local build cache still comes first: the registry is only asked for the
steps that aren't cached locally, so rebuilding the same branch is as fast as
before, offline too.

## Reading the build output

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
