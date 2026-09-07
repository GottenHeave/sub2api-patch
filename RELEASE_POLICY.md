# Release policy

## Entry points

`Patch validation` runs on each `patchset` push, on manual dispatch, and as a
reusable workflow called by `Sync upstream`. Push and manual validation are
read-only.

`Sync upstream` is the only release-capable entry point. It runs on an hourly
schedule or manual dispatch. The complete graph uses the
`sub2api-release-mutation` concurrency group with `cancel-in-progress: false`.

## Upstream selection

Each run fetches `Wei-Shaw/sub2api/main` through the fixed HTTPS read-only
remote. `scripts/upstream-ci-ready.sh` walks its first-parent history and selects
the newest commit whose own completed check runs named `test`, `frontend`, and
`golangci-lint` all concluded with `success`. API or Git query failures stop the
run. No configured SHA limits the search to a historical release base.

## Validation

The reusable validation workflow checks out the selected patchset, fetches the
selected upstream commit, and runs these checks:

1. Workflow wiring smoke tests.
2. Upstream selection, version computation, canonical series, refresh, and
   sanitizer script tests.
3. Canonical patch replay from the selected upstream commit.
4. Patch and release-text reference sanitization.
5. Backend tests.
6. Backend lint and format checks.
7. Frontend frozen-lockfile install, typecheck, and lint.
8. A Docker Buildx build with `push: false` and `load: false`.

The release job is a static dependency of successful resolution and validation.
Validation failures cannot reach publication.

## Publication

The single release job reconstructs the mirror and patched commits from the
resolved inputs. It computes the next `v<upstream-version>-patch.N` version by
querying both tag and GitHub Release namespaces. Failed remote queries stop the
job. If the reconstructed mirror and patched trees already equal the published
branch trees, the job exits before computing or publishing a new version.

Before mutation, the job requires all of the following to be absent:

- the computed Git tag;
- the GitHub Release for that tag; and
- the versioned `ghcr.io/<owner>/sub2api-patch` image tag.

Unexpected HTTP or registry errors are failures, not evidence of absence.

The `main`, `mirror/upstream-main`, and `patched` updates plus the new version
tag are one atomic Git push. Each branch update uses the tip observed during
resolution as its `--force-with-lease` expected value. A changed downstream
branch rejects the whole push. The new tag refspec is not forced.

After the Git push, Docker Buildx publishes the immutable version tag and the
mutable `latest-patch` tag. The workflow then creates the GitHub Release. A
failed publication remains visible as a failed workflow; the workflow does not
discover or reuse prior-run artifacts on rerun.

## Permissions and upstream isolation

Workflows default to `permissions: {}`. Resolution and validation receive only
read permissions. The release job receives downstream `contents: write` and
`packages: write` because it updates this repository, publishes its image, and
creates its release.

No workflow pushes to upstream or sends mutating GitHub API requests to the
upstream repository.
