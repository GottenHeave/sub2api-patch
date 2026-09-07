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
- Only sync upstream commits whose own required upstream checks pass. Check
  results cannot be inherited from a parent commit.

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
`prepare-image-authority`, `authorize-git-mutation`, `mutate-git`, `publish-image`,
and `create-release`;
each mutation depends on successful pre-mutation preparation. `prepare-release`
uploads the validated Docker context, Git publication objects, sanitized notes,
content checksums, and a 28-field canonical manifest without building publication
image bytes. On a fresh run, `prepare-image-authority` performs the sole release
image build: one `linux/amd64` Buildx OCI export with publication and loading
disabled, fixed provenance/SBOM policy, release labels, and the existing GHA cache.
It authenticates to GHCR and records the fresh registry baseline. A tag is absent
only when checksum-pinned ORAS 1.3.0 returns its exact single-line not-found
diagnostic; every other lookup failure is fatal. Recovery retains that recorded
baseline, reuses the previously authorized OCI archive byte for byte, and performs
no build or new registry-baseline login. The authority artifact binds the root
descriptor media type, digest, and size and the recursively complete descriptor/blob
closure, including runtime manifest, config, layers, and every policy-expected
auxiliary descriptor. Each blob
is checked for its content-addressed path, unique presence, size, digest, and
reachability; missing and unreferenced blobs fail authorization. Artifact identities,
archive and closure digests, and the authorized root digest flow only through trusted
`needs` outputs. `publish-image` uses checksum-pinned ORAS and trusted primitives to
copy only that digest-qualified layout. It does not set up or invoke Docker, Buildx,
BuildKit, `buildctl`, a Dockerfile, or another image conversion. The version tag is
copied only when missing, skipped when already exact, and otherwise rejected; exact
readback of its descriptor, raw manifest, and labels precedes `latest-patch`.
`latest-patch` is likewise copied only when needed and always read back.
`create-release` receives the verified digest through `needs` and does not receive
registry credentials.

Every write-capable job downloads its required authority artifact
by exact platform ID and verifies the external archive digest, internal manifest,
content digests, and expected publication state before exposing its scoped write
credential. Those jobs do not check out, clone, or fetch a repository and do not
execute repository-controlled scripts. An `if: always()`
reporter performs authenticated private-GHCR readback and classifies observable
outputs as `completed`, `missing`, `mismatch`, or `unknown`; its diagnostic artifact
is best effort. Same-run recovery independently binds candidate, release, and image
authority artifacts to their producer attempts; image recovery cannot rebuild,
change the platform set or attestation policy, or replace the authorized bytes.

The supported writer of `ghcr.io/<lowercase-owner>/sub2api-patch` tags is this
repository's trusted Sync graph while it holds `sub2api-release-mutation` with
`cancel-in-progress: false`. GHCR and OCI distribution provide no portable tag
compare-and-swap or create-if-absent operation. Organization owners, package
administrators, independently issued PATs, other repositories, and external clients
with package-write access are outside this workflow's enforceable guarantee.
Pre-write checks and final readback detect visible drift, but cannot prove that an
external write was overwritten before readback. See
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
