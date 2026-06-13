# Patch development workflow

All new downstream work must be developed as a patch on top of a known-good base.

## Principles

- Start from an upstream commit whose upstream CI checks are complete and passing.
- Apply the current `patches/cur` patchset first.
- Run the patch validation gates before adding new work.
- Keep the historical baseline patch intact. Add one logically scoped commit per new patch topic after the baseline has been applied and validated.
- Refresh patches from the resulting worktree.
- Do not edit generated branches manually.
- Do not include pull request or issue references in commit subjects, patch files, release notes, or automation comments.

## Add a new patch after the baseline

```sh
# 1. Prepare a clean upstream worktree.
git clone https://github.com/Wei-Shaw/sub2api.git sub2api-worktree
cd sub2api-worktree
git fetch origin main --tags --prune
git checkout -B patch-work origin/main

# 2. Apply current downstream patchset.
../sub2api-patch/scripts/apply-patches.sh .

# 3. Verify the existing base is healthy.
../sub2api-patch/scripts/check-patches.sh .

# 4. Implement the new change and commit it as one logical patch.
git add <changed-files>
git commit -m "feat: describe downstream change without references"

# 5. Re-run checks.
../sub2api-patch/scripts/check-patches.sh .

# 6. Refresh patch files.
../sub2api-patch/scripts/refresh-patches.sh . origin/main
```

## Fix an existing patch

Use a clean worktree, apply all current patches, then amend/fixup the relevant logical commit.
After reordering or squashing, refresh the patch files and run the sanitizer.

## When to split patches

Split new patches when they have different reasons to change or different failure modes. Do not split the historical baseline unless there is a concrete maintenance need and the resulting patchset still applies cleanly. Examples:

- CI/release automation should stay separate from runtime code.
- Schema/migration changes should stay separate from UI-only changes.
- Tests that only document a feature may live with that feature unless they create frequent rebase conflicts.

Avoid splitting only to reduce line count if the resulting patches must always move together.
