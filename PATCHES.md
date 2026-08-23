# Patchset

The current downstream delta is stored as replayable topic patches under `patches/cur`.

The current patchset is based on upstream `v0.1.178` at commit
`e0c48a19ed794a565e3858662520afe0a1f9f0ba`.

Patch topics:

1. downstream Docker image publishing
2. proxy probing and transient network test hardening
3. OpenAI realtime websocket and gateway routing
4. OpenAI audio transcription endpoint support
5. OpenAI realtime REST endpoint support
6. audio usage accounting, billing, persistence, and UI display
7. moderation and settings compatibility
8. remaining proxy repository alignment
9. OpenAI audio transcription retry failure isolation
10. Codex instruction injection control

These patches are synthetic topic patches rebuilt from the final downstream tree, not a raw replay of the original downstream commit history. This is intentional: some original commits predate the latest upstream sync and do not replay cleanly one by one, while the final downstream tree is valid.

The audio transcription topic defers account and model failure side effects until
same-account failover retries are exhausted. A retry that succeeds does not
pollute shared OpenAI account scheduling state.

The Codex instruction topic makes base-prompt injection opt-in through
`enable_codex_instructions_injection`, which defaults to disabled. When
enabled, caller-provided system instructions in Responses, Chat Completions,
or OAuth passthrough suppress the default Codex prompt so supplied prompts are
not duplicated and can retain their provider cache prefix.

Future downstream work should be added as new logical patches after applying and validating the current patchset.

Patch subjects and generated release notes intentionally avoid pull request and issue references.
