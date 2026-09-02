# Patchset

The current downstream delta is stored as replayable capability patches under
`patches/cur`.

The patchset is based on upstream `0.2.0` at commit
`5097b31457e6dc9f49e5f5c9c72b925ce79543b3`.

Patch topics, in replay order:

1. downstream Docker image publication
2. pnpm dependency caching in the Docker BuildKit build
3. singular `input_token_details.cached_tokens` parsing
4. shared endpoint account-selection context and eligibility guards
5. OpenAI realtime WebSocket sessions and translations
6. OpenAI realtime REST sessions, calls, and moderation
7. prompt-audit classification for downstream gateway routes
8. OpenAI audio transcription HTTP transport and routes
9. audio transcription scheduling, retry, diagnostics, and zero-cost usage logging
10. caller-provided Codex system-prompt preservation

These patches are synthetic capability patches rebuilt from the approved final
tree. Patch 4 owns the shared request-context markers and account eligibility
guards used by Realtime REST and transcription. Each capability owns its own
selector, handler, transport, and routes. Patch 7 classifies both capabilities'
routes without implementing either capability.

Selectable dependency closures:

- patch 1 is the standalone Docker publication workflow
- patch 2 is the standalone pnpm BuildKit cache change
- patch 3 is the standalone cached-token parser
- patches 4 through 7 are the complete realtime capability without transcription
- patches 4 and 7 through 9 are the complete transcription capability without realtime
- patch 10 is the standalone Codex prompt-preservation guard

The Docker topics preserve the complete downstream publication workflow and
the pnpm BuildKit cache. The cache-token parser accepts the singular OpenAI
usage field without adding audio-token accounting fields.

The realtime topics provide `/v1/realtime` WebSocket and REST behavior while
preserving Grok behavior on the shared route. The root `/realtime` route remains
Grok-only. Realtime moderation covers live-handler and coordinator relay paths.
Realtime usage retains text and cached-token totals and ignores audio-token
counters.

The transcription topics provide multipart forwarding, account selection,
model mapping, rate-limit handling, retry and failover, redacted diagnostics,
and one best-effort zero-token, zero-cost usage row. They do not add balance or
quota deductions, audio-token schema, pricing, billing, analytics, DTO, Ent,
SQL migration, or frontend changes.

The Codex topic suppresses default prompt injection only when the caller has
already supplied a system prompt. It does not add a setting, admin API, or
frontend control.

Replay and subset evidence:

- all ten patches replay to the same Git tree as integration commit `f50d520a5`
- the Docker workflow and pnpm cache patches apply independently
- the cached-token parser patch passes its focused service test independently
- the realtime closure passes handler, route, security-audit, service, and relay tests without transcription patches
- the transcription closure compiles handler, route, and service packages without realtime patches
- the Codex patch passes focused service tests independently
- the full replay passes `go test ./...`

Future downstream work should be added as a capability-scoped patch after
applying and validating this series.

Patch subjects and generated release notes intentionally avoid pull request and
issue references.
