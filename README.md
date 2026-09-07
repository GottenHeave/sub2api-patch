# sub2api-patch

Patch-based Sub2API downstream.

This repository keeps a small patch series on top of the public upstream
Sub2API source. Automation reads upstream data, applies `patches/cur/*.patch`,
validates the result, and publishes `v<upstream-version>-patch.N` releases.

## Boundaries

- Upstream `Wei-Shaw/sub2api` is read-only.
- Generated text and patches do not mention upstream pull requests, issues, or
  people.
- A selected upstream commit must have successful `test`, `frontend`, and
  `golangci-lint` checks on that commit.
- Versioned tags, images, and GitHub releases are never overwritten.
- `latest-patch` is the mutable image tag for the newest downstream release.

## CI and release flow

Every push to `patchset` runs `Patch validation`. It dynamically selects the
newest eligible commit from the first-parent history of upstream `main`, then
runs the patchset tests and the product gates. This path cannot publish.

`Sync upstream` is the release path. Scheduled and manual runs use one globally
serialized `resolve -> validate -> release` graph:

1. Resolve the current patchset and newest eligible upstream commit, together
   with the current downstream branch tips.
2. Call the same validation workflow used by patchset pushes.
3. In one release job, replay the patches, compute the next version, reject tag,
   image, or release collisions, atomically publish the downstream branches and
   version tag with branch leases, publish the image, and create the release.

If the generated mirror and patched trees already match their published branch
trees, the release job exits without allocating another patch version.

The validation gates are:

- workflow smoke tests;
- upstream selection tests;
- version computation tests;
- canonical patch-series tests;
- patch refresh tests;
- patch reference sanitizer tests;
- patch replay on the selected upstream commit;
- patch reference sanitization;
- backend `go test ./...`;
- `golangci-lint` and format checks;
- frontend frozen-lockfile install, typecheck, and lint; and
- a non-publishing Docker Buildx build.

See [RELEASE_POLICY.md](RELEASE_POLICY.md) for mutation and collision behavior.

## Branches

- `patchset`: patch files, scripts, workflows, and documentation.
- `main`: upstream mirror with upstream workflows removed and downstream patch
  validation installed.
- `mirror/upstream-main`: automation-managed mirror candidate, equal to `main`
  after a successful release.
- `patched`: generated upstream plus the downstream patch series, without
  workflow files.

`main`, `mirror/upstream-main`, and `patched` are automation-managed.

## Local quick check

```sh
scripts/check-patches.sh /path/to/sub2api-worktree
```

For a fixed-base local replay:

```sh
EXPECTED_BASE_SHA=<commit> scripts/apply-patches.sh /path/to/sub2api-worktree
```

`EXPECTED_BASE_SHA` is a local replay guard. Release automation discovers the
current eligible upstream commit dynamically; it does not keep a manually
configured upstream SHA.

## Patch refresh

After resolving conflicts in a worktree based on upstream `main`:

```sh
scripts/refresh-patches.sh /path/to/sub2api-worktree
```

Refresh preserves commit order and emits one patch per commit. Generated
patches are staged and validated before replacing `patches/cur`.
