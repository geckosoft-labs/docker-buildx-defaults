# Developer machines

After [installing the wrapper](../README.md#install), Compose's images are
compressed with zstd. The shared [cache](cache.md) needs two more steps,
once per machine.

1. The containerd image store, which the classic one can't export a cache
   from: Docker Desktop → Settings → General → "Use containerd for pulling
   and storing images"; Docker Engine: `"features":
   {"containerd-snapshotter": true}` in `/etc/docker/daemon.json`, then
   restart it. Images in the old store aren't seen after the switch: the
   first builds are cold.

2. The cache's locations, in the config file (see
   [Settings](../README.md#settings)), e.g.
   `~/.config/docker-buildx-defaults/config` on Linux:

   ```sh
   DOCKER_BUILDX_DEFAULTS_CACHE_FROM=registry.example/dev,registry.example/ci
   DOCKER_BUILDX_DEFAULTS_CACHE_TO=registry.example/dev
   ```

   then `docker login` to their registries. Unlike a shell profile, the
   config file reaches every `docker compose`, also those started by an IDE
   or another app.

A clone records the repository's default branch (`origin/HEAD`), which is
the [fallback branch](cache.md#branches), and the branch is the one checked
out: nothing else to set. `docker buildx docker-buildx-defaults`, in a
checkout, shows what the cache sees:

```
Here: acme/shop (origin remote), branch "feature/x", fallback branch "main"
```

A fallback branch of `""` in a clone made otherwise (e.g. `git init` and
`git remote add`): `git remote set-head origin --auto` records it.

Docker Desktop may put its own `docker-buildx` back in
`~/.docker/cli-plugins` (e.g. on an update): `docker buildx version` then
stops going through the wrapper; install it again.
