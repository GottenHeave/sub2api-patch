# Patchset

The current downstream delta is stored as replayable capability patches under
`patches/cur`. The series is based on upstream `0.2.0` at commit
`5097b31457e6dc9f49e5f5c9c72b925ce79543b3`.

"Upstream status" below means the state of that pinned upstream commit. It does
not claim that a later upstream branch or release has accepted the capability.

## Capability tree

- Distribution
  - Docker publication
    - Patch 1 creates the downstream GHCR publication workflow.
    - It builds the complete application image on pushes to `dev` and publishes
      `dev` and commit-SHA tags.
  - Docker build performance
    - Patch 2 adds a BuildKit cache mount for the pnpm store used by the existing
      frontend build stage.
- OpenAI usage compatibility
  - Cached input tokens
    - Patch 3 accepts the singular OpenAI field
      `input_token_details.cached_tokens` while retaining the existing plural
      field parser.
    - It does not add audio-token accounting.
- Shared OpenAI endpoint scheduling
  - Request classification
    - Patch 4 adds request-context markers for Realtime REST and audio
      transcription selection.
  - Account eligibility
    - Patch 4 limits both endpoint families to API-key and OAuth accounts.
    - It requires explicit account model support for unknown transcription
      models while allowing the known transcription fallback model.
  - Endpoint selectors
    - Patch 4 provides production selectors for Realtime REST and audio
      transcription. Patch 5 specializes the transcription selector with the
      known-model classification owned by that capability.
- OpenAI audio transcription
  - Service transport, scheduling, and resilience
    - Patch 5 owns `ParseOpenAIAudioTranscriptionsRequest`, multipart parsing
      and body rewriting, upstream forwarding, model classification, account
      selection specialization, model-scoped rate limits, retry and failover,
      redacted diagnostics, and usage logging.
  - Handler and routes
    - Patch 6 adds `/v1/audio/transcriptions` and `/transcribe`, request-body
      reading and limits, parser invocation, handler dispatch, route
      registration, and focused handler tests.
    - Patch 6 owns the prompt-audit classifications for both transcription
      routes, so selecting Realtime REST alone does not create stale entries.
  - Billing boundary
    - Patch 5 records one best-effort usage row with zero tokens and zero cost.
      It adds no audio pricing, balance or quota deduction, audio-token schema,
      analytics, DTO, Ent, SQL migration, or frontend changes.
- OpenAI Realtime
  - WebSocket transport
    - Patch 7 adds `/v1/realtime` session and translation WebSocket dispatch,
      upstream URL construction, model mapping, and relay behavior.
    - It preserves Grok dispatch on the shared `/v1/realtime` route and leaves
      the root `/realtime` route Grok-only.
  - REST transport
    - Patch 8 adds session, transcription-session, client-secret, call, and
      translation REST paths, including selection, failover, forwarding, and
      model mapping.
    - Patch 8 also classifies its POST routes as bypassing the Codex Chat and
      Responses prompt transformers. It does not claim WebSocket coverage.
    - Its audit call uses the literal protocol value `openai_realtime`, so the
      transport compiles without the moderation capability.
  - Moderation
    - Patch 9 owns `ContentModerationProtocolOpenAIRealtime`, Realtime event
      extraction, protocol normalization, security-audit extraction, and live
      handler integration.
    - Patch 10 is the single-file integration hook that makes the Realtime REST
      handler use the moderation constant from patch 9.
- Codex prompt handling
  - Caller prompt preservation
    - Patch 11 suppresses default prompt injection only when the caller already
      supplied a non-empty system or developer prompt.
    - It covers transformed, raw passthrough, and Responses-shaped request
      bodies without adding a setting, admin API, or frontend control.

## Patch ownership and upstream status

| Patch | Topic and owned surface | Status in pinned upstream `0.2.0` |
| --- | --- | --- |
| 1 | Downstream Docker image publication; `.github/workflows/docker-ghcr.yml` | Workflow absent |
| 2 | pnpm BuildKit cache mount; `Dockerfile` | Frontend build exists without the pnpm store cache mount |
| 3 | Singular cached-token parsing and focused test; OpenAI response usage service | Parser accepts the plural field but not the singular OpenAI field |
| 4 | Shared endpoint selection context, eligibility guards, and selectors; account scheduler and gateway scheduling | Realtime REST and transcription selection contexts and selectors absent |
| 5 | Audio transcription parser, multipart body rewrite, upstream forwarding, model-aware scheduling, rate limits, retry, failover, diagnostics, and zero-cost usage row | Transcription parsing, transport, scheduling, and service behavior absent; no audio pricing is introduced downstream |
| 6 | Audio transcription handler ingress, body read/limits, parser invocation, routes, route classifications, and tests | `/v1/audio/transcriptions`, `/transcribe`, and their POST route classifications absent |
| 7 | Realtime WebSocket handlers, routes, forwarding, relay, mapping, and tests | OpenAI Realtime WebSocket capability absent; existing Grok route behavior retained |
| 8 | Realtime REST handlers, routes, route classifications, forwarding, mapping, failover, and tests | OpenAI Realtime REST capability and its POST route classifications absent |
| 9 | Realtime moderation protocol, event extraction, security audit, live hook, and tests | Realtime-specific moderation protocol and extraction absent |
| 10 | Realtime REST moderation protocol hook | REST handler absent; no integration hook |
| 11 | Caller-provided Codex system-prompt detection and preservation across request paths | Default prompt injection does not recognize all supported caller prompt shapes |

## Selection closures

Apply selected patches in numerical order. The supported selection map is:

| Selection | Patches | Excludes |
| --- | --- | --- |
| Docker publication | 1 | Application behavior and Dockerfile cache change |
| pnpm BuildKit cache | 2 | Publication workflow and application behavior |
| Singular cached-token parser | 3 | Audio-token pricing and accounting |
| Audio transcription without Realtime | 3, 4, 5, 6 | Realtime transport, moderation, and audio pricing |
| Realtime WebSocket transport | 7 | Realtime REST, moderation, and transcription |
| Realtime transport without moderation | 4, 7, 8 | Realtime moderation and transcription |
| Realtime moderation without REST integration | 7, 9 | Realtime REST transport and its moderation hook |
| Integrated Realtime capability | 4, 7, 8, 9, 10 | Transcription |
| Codex caller-prompt preservation | 11 | Configuration, admin API, and frontend controls |

Supported capability prerequisites are:

- Patch 5 requires patch 4.
- Patch 6 requires patch 5.
- Patch 8 requires patches 4 and 7.
- Patch 9 requires patch 7 to provide a Realtime WebSocket transport.
- Patch 10 requires patches 8 and 9.
- Patches 1, 2, 3, 4, 7, and 11 have no patch prerequisites.

Patch 3 is a policy prerequisite for the transcription closure because it
provides the selected OpenAI usage compatibility behavior and its regression
test. It remains independent of audio pricing and accounting.

## Replay evidence

The complete 11-patch series cleanly replays from the pinned upstream commit.
The replay produces tree
`db5773c6a547bba992cba30430b1ae5fbf208234`, exactly matching approved
integration commit `f50d520a5`.

Verification recorded for that exact final tree includes `go test ./...`.
Independent checks recorded during the rebuild cover Docker publication, the
pnpm cache, the singular cached-token parser, Realtime transport without
transcription, transcription without Realtime, and Codex prompt preservation.
After ordering the transcription service before its ingress and Realtime, and
keeping route classifications in their owning ingress patches, pristine-base
application and focused tests pass for transcription without Realtime
(`3, 4, 5, 6`), Realtime WebSocket transport (`7`), Realtime transport
without moderation (`4, 7, 8`), Realtime moderation without REST (`7, 9`),
and integrated Realtime
(`4, 7, 8, 9, 10`). The focused checks cover
transcription handler, service, and routes; REST handler, service,
`openai_ws_v2`, and routes; moderation handler, security audit, and service;
and the corresponding integrated Realtime packages.

Future downstream work should be added as a capability-scoped patch after
applying and validating this series. Patch subjects and generated release notes
intentionally avoid pull request and issue references.
