# Patchset

The current downstream delta is stored as replayable capability patches under
`patches/cur`.

The patchset is based on upstream `0.2.0` at commit
`5097b31457e6dc9f49e5f5c9c72b925ce79543b3`.

Patch topics, in replay order:

1. downstream Docker image publication
2. pnpm dependency caching in the Docker BuildKit build
3. singular `input_token_details.cached_tokens` parsing
4. OpenAI realtime WebSocket sessions and translations
5. OpenAI realtime REST sessions, translations, and calls
6. moderation of OpenAI realtime client events
7. OpenAI audio transcription HTTP transport and routes
8. audio transcription scheduling, retry, diagnostics, and zero-cost usage logging
9. caller-provided Codex system-prompt preservation

These patches are synthetic capability patches rebuilt from the approved final
tree. Shared gateway route and scheduler files are split into sequential hunks:
patch 4 owns Realtime WebSocket routing, patch 5 owns Realtime REST routing and
selection, and patch 7 owns only transcription routes. Patch 8 extends the
shared scheduler with transcription selection and retry behavior.

Selectable dependency closures:

- patch 1 is the standalone Docker publication workflow
- patch 2 is the standalone pnpm BuildKit cache change
- patch 3 is the standalone cached-token parser
- patches 4 through 6 are the complete realtime capability without transcription
- patches 7 and 8 are the transcription capability in the full ordered series
- patch 9 is the standalone Codex prompt-preservation guard

The transcription scheduler cannot compile as a standalone pair against the
upstream base. The approved final tree expresses its API-key/OAuth account-type
guard in one condition shared with the Realtime REST selection context from
patch 5. Splitting that condition into independent statements would make the
replayed tree differ from the approved integration tree.

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

- all nine patches replay to the same Git tree as integration commit `f50d520a5`
- the Docker workflow and pnpm cache patches apply independently
- the cached-token parser patch passes its focused service test independently
- the realtime closure passes handler, route, security-audit, service, and relay tests without transcription patches
- the Codex patch passes focused service tests independently
- the full replay passes `go test ./...`

Future downstream work should be added as a capability-scoped patch after
applying and validating this series.

Patch subjects and generated release notes intentionally avoid pull request and
issue references.
