# Craft Agent harness automations

These prompts are intended for scheduled Craft Agent automations.

## Upstream watchdog

Cron suggestion: `0 * * * *` UTC.

Prompt:

```text
Check the sub2api-patch repository. Treat Wei-Shaw/sub2api as read-only public upstream data. Do not create, comment on, mention, or reference upstream pull requests, issues, discussions, maintainers, or users. Check whether the latest upstream main commit has complete passing upstream CI. If it does, ensure our mirror/upstream-main branch and internal sync request are up to date. If upstream CI is pending or failing, leave sync paused. Use only upstream tag names and commit SHAs in summaries.
```

## Failure triage

Cron suggestion: `30 */6 * * *` UTC.

Prompt:

```text
Inspect recent failed sub2api-patch workflow runs in our repository only. Do not write to upstream. Do not include pull request or issue references. Download artifacts if available. Classify the failure as patch conflict, backend check failure, frontend check failure, upstream CI pause, sanitizer failure, or release failure. Produce a concise repair report and, if needed, start a focused patch repair session.
```

## Release reconciler

Cron suggestion: `*/30 * * * *` UTC.

Prompt:

```text
Check successful patch validation runs in sub2api-patch that do not have a matching v<upstream-version>-patch.N release. If a release is missing and all gates passed, trigger the release workflow in our repository. Release notes must not include pull request or issue references. Use upstream tag names and commit SHAs only.
```
