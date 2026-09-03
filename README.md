# sub2api-patch

Patch-based Sub2API downstream.

This repository keeps a small, auditable patchset on top of the public upstream
Sub2API source. Automation reads upstream data only, applies `patches/cur/*.patch`,
runs checks, and publishes `v<upstream-version>-patch.N` releases only through the
trusted `Sync upstream` workflow after all gates pass.

## Hard boundaries

- Do not write to upstream.
- Do not create upstream pull requests, issues, comments, reviews, or discussions.
- Do not mention upstream maintainers or users in generated text.
- Do not include pull request or issue references in patches, comments, or release notes.
- Use upstream tags and commit SHAs only.
- Only sync upstream commits whose required upstream checks pass, except for the
  single VERSION-only `[skip ci]` inheritance case documented in
  [RELEASE_POLICY.md](RELEASE_POLICY.md).

## Remote validation

Every push to `patchset` starts `Patch validation`. The run is pinned to the
immutable pushed commit from `github.event.after`; a manual validation is pinned
to the event's immutable `github.sha`. Neither path follows a moving branch after
the run starts.

Validation independently fetches `Wei-Shaw/sub2api` with read-only credentials and
walks the first-parent history of `main` to select the newest CI-ready upstream
commit. It applies the canonical patch series to that pristine commit and records:

- `patchset_sha`: the exact patch metadata commit being validated;
- `upstream_source_sha`: the selected pristine upstream replay base;
- `mirror_candidate_sha`: empty for push and manual validation;
- `replay_tree_sha`: the tree produced by canonical patch replay; and
- the originating repository, workflow path, run ID, run attempt, and conclusion.

All nonempty SHA values are full lowercase 40-character object names. Push and
manual runs are validation-only: they cannot update branches or pull requests,
create tags or releases, or publish packages or images. A payload field, input,
artifact, branch name, or prior run cannot grant release authority.

The validation gates are identical to the common gates used by a release-capable
sync: exact-base patch replay, reference sanitization, backend tests, configured Go
lint and format checks, frozen-lockfile frontend install plus typecheck and lint,
and a non-publishing Docker Buildx preflight. The complete provenance bundle is
written to the run summary and validation artifact.

`Sync upstream` is the sole release-capable entry point. Its schedule or manual
dispatch runs one static job graph under the repository-wide
`sub2api-release-mutation` concurrency group. The graph resolves immutable source
identities, creates a local two-parent mirror candidate, calls the same validation,
and may call reusable `Auto release` only through successful `needs` dependencies
and explicit `release_after_validation=true` intent. `Auto release` has no direct
trigger. Its release continuation is ordered as `prepare-release`,
`prepare-image-authority`, `mutate-git`, `publish-image`, and `create-release`;
each mutation depends on successful pre-mutation preparation. `prepare-release`
uploads the exact preflighted Docker context, Git publication objects, sanitized
notes, content checksums, and a 28-field canonical manifest.
`prepare-image-authority` builds the complete `linux/amd64` image before mutation,
records the authenticated private-GHCR baseline in a 20-field manifest, and uploads
the OCI layout as a second immutable artifact. Artifact identities, archive and
internal digests, and the authorized OCI manifest digest flow only through trusted
`needs` outputs. `publish-image` copies that digest-qualified OCI layout to both
tags with ORAS. `create-release` receives the verified digest through `needs` and
does not receive registry credentials.

Every write-capable job downloads its required release or image-authority artifact
by exact platform ID and verifies the external archive digest, internal manifest,
content digests, and expected publication state before exposing its scoped write
credential. Those jobs do not check out, clone, or fetch a repository and do not
execute repository-controlled scripts. An `if: always()`
reporter performs authenticated private-GHCR readback and classifies observable
outputs as `completed`, `missing`, `mismatch`, or `unknown`; its diagnostic artifact
is best effort. Same-run recovery independently binds candidate, release, and image
authority artifacts to their producer attempts. See
[RELEASE_POLICY.md](RELEASE_POLICY.md) for the upstream CI exception, mutation
boundaries, retry rules, and release outputs.

## Branches

- `patchset`: patch files, scripts, workflows, and documentation.
- `main`: upstream mirror branch in this repository, with upstream workflow files removed and the downstream patch validation workflow retained.
- `mirror/upstream-main`: automation-managed upstream mirror candidate with the same workflow policy as `main`.
- `patched`: generated branch containing upstream plus this patchset, with workflow files removed before release push.

`patched` and `mirror/upstream-main` are generated. Do not commit to them manually.
Automation records the raw `upstream_source_sha` separately and verifies the
generated mirror candidate's parent chain; branch names and publication trees are
never substituted for that source identity.

## Local quick check

```sh
scripts/check-patches.sh /path/to/sub2api-worktree
```

Patch application rejects worktrees with tracked or untracked changes. For a
fixed-base replay, require an exact starting commit:

```sh
EXPECTED_BASE_SHA=<commit> scripts/apply-patches.sh /path/to/sub2api-worktree
```

## Patch refresh

After resolving conflicts in a worktree based on upstream/main:

```sh
scripts/refresh-patches.sh /path/to/sub2api-worktree
```

Refresh also rejects uncommitted changes. Set `EXPECTED_BASE_SHA` to require
the resolved base ref to match a specific commit before replacing patch files.
Refresh preserves commit order and emits one patch per commit, so the worktree
must contain a curated logical commit series with one capability topic per
commit. Generated patches are staged and validated before the current series
is replaced; rejected output leaves `patches/cur` unchanged.
