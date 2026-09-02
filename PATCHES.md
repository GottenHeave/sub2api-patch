# Patchset

The current downstream delta is stored as replayable topic patches under `patches/cur`.

The current patchset is based on upstream `main` at commit
`0d27f45ead1b58908548ec21afd923ecaf7339bc` (`v0.1.185`).

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
11. v0.1.179 compatibility alignment
12. structured Codex system-prompt preservation
13. v0.1.185 compatibility and lint alignment
14. OpenAI audio transcription upstream diagnostics
15. OpenAI audio transcription model selection
16. OpenAI audio transcription model-mapping bypass

These patches are synthetic topic patches rebuilt from the final downstream tree, not a raw replay of the original downstream commit history. This is intentional: some original commits predate the latest upstream sync and do not replay cleanly one by one, while the final downstream tree is valid.

The audio transcription topic defers account and model failure side effects until
same-account failover retries are exhausted. A retry that succeeds does not
pollute shared OpenAI account scheduling state.

The Codex instruction topic makes base-prompt injection opt-in through
`enable_codex_instructions_injection`, which defaults to disabled. When
enabled, caller-provided system instructions in Responses, Chat Completions,
or OAuth passthrough suppress the default Codex prompt so supplied prompts are
not duplicated and can retain their provider cache prefix.

The v0.1.179 compatibility topic keeps the downstream tree aligned with the
release's long-context pricing gate, where enabling the group applies the
long-context multipliers. The v0.1.185 compatibility topics preserve upstream
usage-log field ordering, scheduler API signatures, realtime model mapping,
and the release's formatting and lint requirements.

The structured Codex system-prompt topic keeps raw and decoded request checks
consistent for string and structured prompt values. A caller-provided system
prompt therefore suppresses the optional default prompt before and after OAuth
request transformation, preserving the caller's prompt prefix for caching.

The audio transcription diagnostics topic records failover-eligible upstream
HTTP responses and transport/read failures with account, model, status, request
ID, and sanitized response details. It does not log uploaded audio or
authorization headers, and does not change VoiceInk request or response
protocol behavior.

The audio transcription model selection topic allows known transcription
models to use an otherwise eligible OpenAI account even when its mapping lists
only chat models. The bypass applies on the first selection and account
switches, while account type, schedulability, model rate limits, channel
restrictions, endpoint capabilities, and profit controls remain enforced.
Unknown audio models and other endpoints keep their model allowlists.

Future downstream work should be added as new logical patches after applying and validating the current patchset.

Patch subjects and generated release notes intentionally avoid pull request and issue references.
