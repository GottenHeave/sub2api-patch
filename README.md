# sub2api-patch

Patch-based Sub2API downstream.

This repository keeps a small, auditable patchset on top of the public upstream
Sub2API source. Automation reads upstream data only, applies `patches/cur/*.patch`,
runs checks, and publishes `v<upstream-version>-patch.N` releases when all gates pass.

## Hard boundaries

- Do not write to upstream.
- Do not create upstream pull requests, issues, comments, reviews, or discussions.
- Do not mention upstream maintainers or users in generated text.
- Do not include pull request or issue references in patches, comments, or release notes.
- Use upstream tags and commit SHAs only.
- Only sync upstream commits whose upstream CI checks are complete and passing.

## Branches

- `patchset`: patch files, scripts, workflows, and documentation.
- `main`: clean upstream mirror branch in this repository.
- `mirror/upstream-main`: automation-managed upstream mirror candidate.
- `patched`: generated branch containing upstream plus this patchset.

`patched` and `mirror/upstream-main` are generated. Do not commit to them manually.

## Local quick check

```sh
scripts/check-patches.sh /path/to/sub2api-worktree
```

## Patch refresh

After resolving conflicts in a worktree based on upstream/main:

```sh
scripts/refresh-patches.sh /path/to/sub2api-worktree
```
