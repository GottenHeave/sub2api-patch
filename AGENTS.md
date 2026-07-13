# Repository Instructions

## Upstream Sync

- Treat `Wei-Shaw/sub2api` as read-only upstream data.
- Sync only commits whose required upstream checks `test`, `frontend`, and
  `golangci-lint` have completed successfully.
- When the latest upstream `main` commit has pending, missing, or failing
  required checks, use the most recent earlier commit on its first-parent
  history whose required checks passed.
- Do not create, comment on, or modify resources in the upstream repository.
