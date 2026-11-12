# CI

On runners and build agents, install the release binary as
`/usr/local/lib/docker/cli-plugins/docker-buildx` (Linux), which the docker
CLI searches before the package's `/usr/libexec/docker/cli-plugins`. For the
[cache](cache.md), Docker Engine needs the containerd image store:
`"features": {"containerd-snapshotter": true}` in
`/etc/docker/daemon.json`, then restart it.

In the config file of the user the jobs run as (or in their environment),
set:

- `DOCKER_BUILDX_DEFAULTS_CACHE_FROM` and `DOCKER_BUILDX_DEFAULTS_CACHE_TO`,
  with the docker login of the registry's CI user;
- `DOCKER_BUILDX_DEFAULTS_PUSH_COMPRESSION=zstd` (and
  `DOCKER_BUILDX_DEFAULTS_PUSH_FORCE_COMPRESSION=true`) if what pulls the
  images reads zstd (see [Compression](compression.md)).

```sh
DOCKER_BUILDX_DEFAULTS_CACHE_FROM=registry.example/ci
DOCKER_BUILDX_DEFAULTS_CACHE_TO=registry.example/ci
DOCKER_BUILDX_DEFAULTS_PUSH_COMPRESSION=zstd
```

What CI's checkouts lack is the [branches](cache.md#branches): some check
out a commit rather than a branch, and none record the repository's default
branch (`origin/HEAD`), which is the fallback. Some leave out `.git`
altogether, and with it the `origin` remote the refs are named after.

- [GitHub Actions](github-actions.md) names them in its own variables:
  nothing to set.
- [TeamCity](teamcity.md): the fallback branch, and with the server-side
  checkout the branch and the cache prefix too.
- Elsewhere: `DOCKER_BUILDX_DEFAULTS_BRANCH` where the checkout isn't of a
  branch, `DOCKER_BUILDX_DEFAULTS_FALLBACK_BRANCH`, and
  `DOCKER_BUILDX_DEFAULTS_CACHE_PREFIX` where the checkout has no `origin`
  remote; all three in the job's environment.

`docker buildx docker-buildx-defaults`, run in a job's checkout, shows what
the cache sees there.
