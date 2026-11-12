# Compression

BuildKit compresses layers with gzip, and has no setting for another default.
The wrapper adds `compression=zstd`:

- with `DOCKER_BUILDX_DEFAULTS_COMPRESSION=zstd` (the default), to the image
  outputs that load into the local image store, which is where Compose's
  builds go (`type=docker`, or `type=image` without push), and to the exports
  of the [cache](cache.md), when there's one;
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
