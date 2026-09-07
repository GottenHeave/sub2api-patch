# Release policy

## Validation and release authority

`Patch validation` has two validation-only entry points:

- a push to `refs/heads/patchset`, pinned to `github.event.after`; and
- a manual dispatch, pinned to the event's immutable `github.sha`.

Both entry points independently fetch `Wei-Shaw/sub2api/main` read-only, select an
eligible upstream commit, and validate the exact patchset revision. They cannot
release, even when an event payload contains `release_after_validation` or a
similarly named field. They cannot dispatch `Auto release`, update
`mirror/upstream-main`, `main`, or `patched`, mutate a pull request, create a tag or
release, write package state, or publish an image.

The sole release-capable path is one top-level `Sync upstream` run triggered by its
schedule or its own manual dispatch. That run owns candidate construction,
validation, and release continuation in a static `needs` graph. It resolves all
source identities once, constructs the mirror candidate locally, invokes reusable
validation, and may invoke reusable `Auto release` only when every dependency
succeeds and trusted Sync intent is explicitly `release_after_validation=true`.
The boolean expresses intent; successful dependencies and recomputed provenance
grant authority. `Auto release` has no standalone trigger.

## Upstream candidate policy

Selection walks the first-parent history of `Wei-Shaw/sub2api/main` and returns the
newest eligible commit. Every candidate must have completed check runs named
`test`, `frontend`, and `golangci-lint`, each with conclusion `success`. Missing,
pending, queued, `neutral`, `skipped`, `cancelled`, `timed_out`,
`action_required`, `failure`, `stale`, and every other non-success state are
ineligible.

Check results belong only to the commit on which they ran. A candidate cannot
inherit a parent's results, including when it changes only
`backend/cmd/server/VERSION` or contains `[skip ci]`.

## Immutable provenance

Every validation result contains one immutable provenance bundle:

- `patchset_sha`: the exact downstream patch metadata commit. It is
  `github.event.after` for a `patchset` push, the immutable `github.sha` for manual
  validation, and the once-resolved `origin/patchset` commit for trusted Sync.
- `upstream_source_sha`: the raw first-parent upstream commit selected under the
  policy above. Canonical replay starts here, and `EXPECTED_BASE_SHA` enforces it.
- `mirror_candidate_sha`: the two-parent promotion candidate constructed only by
  trusted Sync. Its second parent is the downstream workflow-install commit, whose
  parent is `upstream_source_sha`. Validation-only runs record an empty value.
- `replay_tree_sha`: the Git tree produced by applying the canonical series to
  `upstream_source_sha`.

The bundle also records the originating repository, workflow path, run ID, run
attempt, and validation conclusion. Nonempty SHA fields are full lowercase
40-character object names. The raw source commit, workflow-install commit, and
mirror candidate retain distinct identities even when their trees match. Artifacts,
caller-provided SHAs or booleans, run IDs, tags, and branch names do not confer
authority.

## Common validation gates

Push, manual, and trusted Sync validation run the same gates against exact commits:

1. Apply the canonical series to pristine `upstream_source_sha`, enforced through
   `EXPECTED_BASE_SHA`.
2. Run the patch/reference sanitizer against that exact replay base.
3. Run backend `go test ./...`.
4. Run the patched tree's configured Go lint and format checks.
5. Install frontend dependencies from the frozen lockfile, then run typecheck and
   lint.
6. Run Docker Buildx against the release Dockerfile and context with `push: false`,
   `load: false`, and GHA cache scope `sub2api-patch-release`.
7. Record the complete provenance bundle in an artifact and the job summary.

No common gate is skipped for a push. Any failure prevents release continuation.

## Validation-before-mutation graph

Before Git mutation, trusted Sync completes upstream selection, provenance and
parent-chain checks, exact checkout, patch replay, all common gates, replay-tree
equality, clean-tree/unmerged-path/rejected-residue checks, `git diff --check`,
version computation, local and remote tag/release queries, release-note generation
and sanitization, publication commit construction and checks, image-owner
calculation, and the common non-publishing Docker validation preflight. That common
gate tests the Dockerfile and context; it is separate from the single release OCI
export described below.

`prepare-release` packages the validated Docker context, Git publication
objects, sanitized notes, and their content checksums. Its canonical 28-field
manifest records the complete provenance, expected refs, replay and publication
identities, version, image tags, OCI labels, Docker context and tree, Dockerfile and
cache settings, and note and content digests. The artifact upload's immutable
platform `artifact_id` and `artifact_digest` remain external to the payload. Those
values and the internal manifest digest flow only through successful trusted
`needs` outputs.

`prepare-image-authority` runs after `prepare-release` and before any mutation. On a
fresh run it performs the sole release publication build: exactly one
`linux/amd64` Buildx OCI export with `push: false`, `load: false`, fixed
`provenance: false` and `sbom: false`, release labels, and the existing GHA cache.
It then authenticates to private GHCR with package-read permission and records the
observed version and `latest-patch` registry state. A tag is classified as missing
only when checksum-pinned ORAS 1.3.0 emits exactly one stderr line matching
`Error response from registry: failed to find "<tag>": <tag>: not found`; every
other lookup failure is fatal. Recovery downloads the prior authority, retains its
recorded baseline, and skips registry-baseline login, Buildx, and image construction.
The immutable artifact contains the exact OCI archive, a canonical closure document,
and fixed authority metadata.
The closure binds the root descriptor's media type, digest, size, and bytes; each
runtime platform descriptor and variant; its config and layers; and every recursively
reachable policy-expected auxiliary descriptor and blob. Verification rejects
duplicate, unsafe, or traversing archive paths, missing or extra platforms, missing
or unreferenced blobs, conflicting descriptors, and any path, byte size, or digest
mismatch. Fresh redownload and recovery independently regenerate the canonical
closure and require byte-for-byte equality before exposing its digest through
trusted `needs` outputs.

The release continuation is `prepare-release` -> `prepare-image-authority` ->
`authorize-git-mutation` -> `mutate-git` -> `publish-image` -> `create-release`,
with each job reachable only through successful `needs` dependencies. Each
write-capable job downloads its
required raw artifact by the exact trusted platform ID, requires its SHA-256 to
equal the external platform digest, verifies internal manifests and bundled content
digests, and matches all trusted fields to `needs` outputs. None runs
`actions/checkout`, clones or fetches a repository, or executes a
repository-controlled script or hook. Before authorization, none executes bundled
executable content, including the Dockerfile. Artifact selection and verification
use the run-scoped artifact channel and trusted inline commands without a
repository, Git, registry, or package write credential.

Only after artifact authorization does `mutate-git` expose its GitHub credential,
perform final provenance, source, tree, target-ref, release, and tag rechecks, and
publish the mirror candidate, create or update the internal sync request, promote
`main`, and atomically update `patched` while reserving the version tag. Ref updates
use their validated old values or an equivalent compare-and-swap guard.

`publish-image` verifies the pre-mutation authority artifact and published
branch/tag identities before exposing its GHCR credential. It uses only
checksum-pinned ORAS and trusted runner primitives after authorization. It cannot
set up or run Docker, Buildx, a Docker build action, BuildKit, `buildctl`, a
Dockerfile, or another OCI rebuild or conversion. It queries the version tag
immediately before publication. A missing tag is copied from the exact
digest-qualified layout, an exact authorized tag is skipped, and every other digest
fails; either successful path requires readback of the descriptor media type, digest
and size, raw manifest bytes, config digest and size, and labels. Only after that
version readback succeeds does it apply the same conditional copy-or-skip rule to
`latest-patch` and require its exact readback. That digest flows through the
successful `publish-image` dependency.

`create-release` independently verifies the release artifact, branch, tag, notes,
and existing-release state. It compares the digest reported by `publish-image` with
the digest authorized by `prepare-image-authority`; it performs no registry reads,
has no package permission, and receives no registry credential. Only then does it
expose its contents token to the single release creation command. An exact existing
release is skipped; a mismatch fails without overwrite.

## Concurrency and permissions

The complete release-capable Sync graph holds the repository-wide
`sub2api-release-mutation` concurrency group with `cancel-in-progress: false` from
before its first mutation through its final branch, tag, release, package, and image
operation. A waiting Sync has no candidate or release authority. Once a Sync starts,
a later event cannot cancel its authorized continuation.

Push and manual runs use
`sub2api-patch-validation-${patchset_sha}` and may cancel an older validation of the
same patchset SHA. They are read-only, may overlap a release, cannot acquire the
mutation group, and cannot provide release authority.

Affected workflows default to `permissions: {}` or an equivalent read-only default.
Validation jobs never receive `actions: write`, `contents: write`,
`packages: write`, `pull-requests: write`, or `id-token: write`. In `Auto release`,
`prepare-release` has read access to actions, contents, and checks;
`prepare-image-authority` has read access to actions, contents, and packages;
`mutate-git` has write access to contents and pull requests plus read access to
actions and checks; `publish-image` has package write access plus read access to
actions; and `create-release` has contents write access plus actions read access.
The always-running reporter has read access to actions, contents, and packages and
authenticates before private-GHCR inspection. No job receives `id-token: write`.
Write credentials are supplied only to the post-authorization operation that needs
them.

Before fetching, automation verifies that the upstream repository and remote URL
are exactly `Wei-Shaw/sub2api`. Upstream credentials allow API reads and Git fetches
only. No push, mutating API request, pull-request or comment operation, release, or
package/image publication may target upstream.

## Versions, collisions, and release outputs

Release versions use:

```text
v<upstream-version>-patch.<counter>
```

Fresh version computation fetches the complete relevant
`v<upstream-version>-patch.N` tag and release namespaces and requires their sets to
be identical in both directions. Any tag without a matching release or release
without a matching tag is fatal, including an orphan below the highest counter. The
counter starts at 1 when both sets are empty and otherwise increments past their
shared highest counter. Remote tags and releases are refreshed and checked again
immediately before the first mutation. A lookup failure is fatal; it is never
treated as absence. A new release version must be absent from the fetched local
tags, remote tags, and releases.

A full GitHub rerun retains the run ID and may resume one reserved tag without a
release only when it occupies the next completed-release counter and exact same-run
artifacts match every provenance and publication field. Candidate, release, and
image-authority artifacts are selected independently from the highest available
prior producer attempt. Candidate provenance remains bound to its candidate
producer attempt; release provenance remains bound to its release producer attempt;
image authority remains bound to its own producer attempt. Recovery never requires
those attempts to be equal. Recovery with an image authority reuses its exact OCI
archive and root digest; it cannot rebuild, select another platform, or change the
provenance/SBOM policy. A rerun without a release artifact validates under the
current attempt and first proves the expected `main`, `mirror/upstream-main`, and
`patched` refs are unchanged. Only exact same-run recovery bound to a verified
release artifact bypasses fresh computation and retains its reserved version after
proving that version is the next counter after the matched completed namespace. A
newly dispatched run has no same-run artifact authority and fails on any orphan
rather than choosing the next counter or silently abandoning partial publication.

Release notes contain the computed version, upstream version, and full
`upstream_source_sha`, `patchset_sha`, and `replay_tree_sha`, followed by this exact
ordered topic list:

1. publish downstream Docker images;
2. cache pnpm dependencies with BuildKit;
3. parse singular OpenAI cached token details;
4. add shared endpoint account scheduling context;
5. schedule resilient OpenAI audio transcriptions;
6. expose OpenAI audio transcription transport;
7. proxy OpenAI realtime WebSocket sessions;
8. proxy OpenAI realtime REST endpoints;
9. moderate OpenAI realtime client events;
10. connect realtime REST moderation protocol; and
11. preserve caller Codex system prompts.

The notes and generated commit metadata are sanitized before mutation. The patched
publication commit removes upstream workflow files, changes only those intended
workflow paths, has the validated patched commit as its parent, and records its
publication tree separately from `replay_tree_sha`.

A successful release publishes the mirror candidate, creates or updates the
internal sync request, updates `main`, atomically updates `patched` and reserves the
computed tag, publishes both images, and finally creates a GitHub release titled
with that tag and the sanitized notes. The image tags are:

```text
ghcr.io/<lowercase-owner>/sub2api-patch:<VERSION>
ghcr.io/<lowercase-owner>/sub2api-patch:latest-patch
```

Both images use `org.opencontainers.image.version=<VERSION>` and
`org.opencontainers.image.revision=<validated-patched-commit-sha>` labels and the
existing `sub2api-patch-release` GHA cache-from/cache-to configuration.

`prepare-image-authority` records authenticated observations of both image tags,
including the `latest-patch` manifest digest and OCI labels when present. A prior
release image is an authorized starting state only when its version has both a
release and reserved tag and its revision identifies that tagged publication commit
or its direct replay parent. Publication accepts that exact recorded state, the
absent state recorded by preparation, or the exact target image produced by a
partial retry. Any other change fails before an image write. A missing version image
with the exact target `latest-patch` image is restored from the authorized OCI
layout; two exact target images are skipped; and a new version replaces the recorded
historical `latest-patch` while publishing both tags.

The only supported writer of these GHCR tags is this repository's trusted Sync path
while it holds the global `sub2api-release-mutation` group with
`cancel-in-progress: false`. Workflow and package administration must restrict the
package-write credential to that path. GHCR and OCI distribution provide no portable
tag compare-and-swap or create-if-absent operation. Organization owners, package
administrators, independently issued PATs, other repositories, and external registry
clients with package-write access are outside the enforceable workflow guarantee.
Immediate pre-write checks, digest-qualified sources, and exact readback detect
visible conflicting changes. They cannot prove that an external writer's update was
overwritten before final readback, so this policy does not claim atomic CAS or
race-free publication against those writers.

## Partial publication and reporting

Retries remain bound to the exact candidate, release, and image-authority artifact
IDs, their producer attempts and external archive digests, the internal digests,
the 28-field release manifest, and the canonical image-authority metadata and full
OCI descriptor/blob closure. Image retry reuses the authority archive byte for byte
and cannot rebuild or regenerate image inputs. Already
completed mirror, main, patched, tag, image, and release outputs are skipped only
when their exact identities match. Missing outputs resume in Git/tag, image, then
release order. Any ref, image label or manifest, release notes, digest, or provenance
mismatch fails without overwrite or input regeneration.

`report-publication-outcome` runs with `if: always()` and read-only permissions. It
independently resolves and verifies the image-authority artifact, authenticates to
private GHCR, and reads back both tags and their labels. For observable workflow
execution it records both preparation jobs, Git authorization, Git mutation, image,
and release job results and classifies each branch, tag, image, and GitHub release
as `completed`, `missing`, `mismatch`, or `unknown`. It reports `complete` only when all six jobs
succeeded and every output is an exact match; otherwise it reports `incomplete`.
Summary generation and diagnostic artifact upload are best effort, so runner
allocation failure, cancellation, or an artifact-service outage can leave only the
native non-success conclusion.
