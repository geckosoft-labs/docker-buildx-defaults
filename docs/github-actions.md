# GitHub Actions

Set up the runner as in [CI](ci.md). The branches come from the job's
variables, with nothing to set:

- the branch: the checked-out one, else `GITHUB_HEAD_REF` (the source branch
  of a pull request), else `GITHUB_REF_NAME` when the run is for a branch. A
  tag's build imports the fallback branch and exports nothing.
- the fallback branch: the one a pull request merges into
  (`GITHUB_BASE_REF`), else the repository's default, from the job's event
  (`GITHUB_EVENT_PATH`).

The fallback comes from these variables only when the checkout is of the
workflow's repository (its `origin` remote): a job can check out others, and
their builds then have no fallback branch, unless
`DOCKER_BUILDX_DEFAULTS_FALLBACK_BRANCH` names it.

Only bakes get the defaults: `docker compose build` and `docker buildx bake`
do, `docker/build-push-action` (which runs `docker buildx build`) doesn't.
