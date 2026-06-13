# Patchset

The current downstream delta is stored as replayable topic patches under `patches/cur`.

Patch topics:

1. downstream Docker image publishing
2. proxy probing and transient network test hardening
3. OpenAI realtime websocket and gateway routing
4. OpenAI audio transcription endpoint support
5. OpenAI realtime REST endpoint support
6. audio usage accounting, billing, persistence, and UI display
7. moderation and settings compatibility
8. remaining proxy repository alignment

These patches are synthetic topic patches rebuilt from the final downstream tree, not a raw replay of the original downstream commit history. This is intentional: some original commits predate the latest upstream sync and do not replay cleanly one by one, while the final downstream tree is valid.

Future downstream work should be added as new logical patches after applying and validating the current patchset.

Patch subjects and generated release notes intentionally avoid pull request and issue references.
