# TeamCity

Set up the agents as in [CI](ci.md). What else a build needs depends on the
VCS root's checkout mode:

| | Agent-side checkout | Server-side checkout |
|---|---|---|
| `env.DOCKER_BUILDX_DEFAULTS_FALLBACK_BRANCH` | recommended | recommended |
| `env.DOCKER_BUILDX_DEFAULTS_BRANCH` | no | `%teamcity.build.branch%` |
| `env.DOCKER_BUILDX_DEFAULTS_CACHE_PREFIX` | no | the repository's `<org>/<repo>` |
| Branch specification naming the default branch | no | e.g. `+:refs/heads/(*)` |

## Agent-side checkout

The checkout is a git clone with the `origin` remote, on a local branch of
the branch being built (the default branch's own name, not `<default>`): the
wrapper finds the branch and the refs' `<org>/<repo>` there. In an agent's
checkout directory:

```
Here: acme/shop (origin remote), branch "feature/x", fallback branch ""
```

TeamCity's clones don't record the repository's default branch
(`origin/HEAD`), which is the fallback: without
`env.DOCKER_BUILDX_DEFAULTS_FALLBACK_BRANCH`, a branch's first builds import
nothing. Set it in the repository's project, for its builds, to either:

- the default branch's name, e.g. `main`;
- `%vcsroot.branch%`, the VCS root's default branch, when each of the
  project's build configurations has one VCS root; in a configuration with
  several the reference doesn't resolve, and TeamCity doesn't start its
  builds.

`refs/heads/` is dropped from both settings.

Don't set `env.DOCKER_BUILDX_DEFAULTS_BRANCH` for these builds: it comes
before the checked-out branch, and `%teamcity.build.branch%` is `<default>`
for the default branch's builds, unless the branch specification names it
(see below).

## Server-side checkout

The agent gets the files without `.git`: no branch, no `origin` remote. In
the projects that check out this way, set:

- `env.DOCKER_BUILDX_DEFAULTS_CACHE_PREFIX` to the repository's
  `<org>/<repo>`, as its `origin` remote would name it, so that its builds
  share the cache of agent-side and developers' builds; without it, they get
  no cache;
- `env.DOCKER_BUILDX_DEFAULTS_BRANCH` to `%teamcity.build.branch%`, the
  build's logical branch name; `refs/heads/` is dropped. The VCS root's
  branch specification must name the default branch too, with logical names
  that are the branches' own, e.g. `+:refs/heads/(*)`: otherwise its builds
  get `<default>`, which needs `origin/HEAD`, and export nothing, with a
  warning;
- the fallback branch, as for the agent-side checkout.

With a project of each:

```
Project "shop" (agent-side checkout)
  env.DOCKER_BUILDX_DEFAULTS_FALLBACK_BRANCH = main

Project "blog" (server-side checkout)
  env.DOCKER_BUILDX_DEFAULTS_FALLBACK_BRANCH = main
  env.DOCKER_BUILDX_DEFAULTS_CACHE_PREFIX = acme/blog
  env.DOCKER_BUILDX_DEFAULTS_BRANCH = %teamcity.build.branch%
```
