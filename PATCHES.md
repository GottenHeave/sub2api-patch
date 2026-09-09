# Patchset

Last manually checked release: `v0.2.4-patch.1` on 2026-09-09. That review
checked CI and publication results, not a live Codex/OpenAI session. Automation
continues selecting CI-eligible upstream commits; this page is a manual
snapshot and does not need updating for every automated release.

The patch series was prepared against upstream `v0.2.2`. That is its authoring
base, not a version pin for future syncs or a claim about the latest release.

## Patch inventory

Sizes below were counted on 2026-09-09 from the stored `patches/cur/*.patch`
files using `git apply --numstat`. They include tests; `+` and `-` are added
and deleted lines. Paths are relative to upstream, with backend paths under
`backend/internal/`. File counts are per patch, so shared files appear in
more than one row. This table is maintained manually, not enforced by CI.

| Patch | Purpose | Affected scope | Files | Diff |
| --- | --- | --- | ---: | ---: |
| 1: Docker publication | Publish application images for `dev` pushes; stripped from generated release branches with all other workflows | `.github/workflows/docker-ghcr.yml` | 1 | +49 / -0 |
| 2: pnpm cache | Mount the pnpm store cache in the frontend build stage | `Dockerfile` | 1 | +1 / -1 |
| 3: Cached-token compatibility | Accept singular `input_token_details.cached_tokens` alongside the plural field | `service/openai_gateway_response_handling.go` and usage test | 2 | +20 / -0 |
| 4: Endpoint scheduling | Share model-aware account selection for STT and Realtime REST, accepting API-key and OAuth accounts | `service/openai_account_scheduler.go`, `openai_gateway_scheduling.go` | 2 | +121 / -2 |
| 5: STT service | Parse and forward transcription requests, map models, retry/fail over, redact diagnostics, and record zero-cost usage | `service/openai_audio_transcriptions*`, scheduler, model rate limits, usage logging | 5 | +1773 / -5 |
| 6: STT routes | Expose `/v1/audio/transcriptions` and `/transcribe`, with body limits and handler dispatch | `handler/openai_audio_transcriptions*`, `server/routes/gateway.go` and route coverage test | 4 | +1017 / -4 |
| 7: Realtime WebSocket | Relay sessions and translations, preserve caller model aliases and protocol headers, support OAuth call sideband, retain Grok routing | Gateway handler, routes, scheduler, `service/openai_ws_*`, relay tests | 16 | +923 / -102 |
| 8: Realtime REST | Forward sessions, client secrets, calls and translations; map models before scheduling, retry/fail over, bind calls to accounts | `handler/endpoint.go`, `handler/openai_realtime_calls*`, `service/openai_realtime_calls*`, routes and tests | 8 | +1742 / -1 |
| 9: Realtime moderation | Extract Realtime text/images for moderation and security audit, including Live requests | `handler/openai_live*`, `securityaudit/*`, `service/content_moderation*` | 7 | +243 / -1 |
| 10: REST moderation hook | Use the shared Realtime moderation protocol in the REST handler | `handler/openai_realtime_calls.go` | 1 | +1 / -1 |
| 11: Caller prompts | Preserve non-empty system or developer prompts in transformed, raw passthrough and Responses-shaped requests | `service/openai_codex_transform.go`, gateway forward/passthrough/request-body helpers and prompt tests | 5 | +313 / -14 |

## Behavior boundaries

- STT supports API-key and OAuth accounts. Its usage row has zero tokens and
  zero cost; it adds no audio pricing, balance/quota deductions, schema, or UI.
- Realtime standalone WebSocket sessions use API-key accounts. OAuth WebRTC
  calls use the Codex backend endpoint, then retain the selected account's
  bearer and account ID for the `call_id` sideband.
- Explicit `quicksilver=v1` calls use the Realtime REST handler. Default and
  `quicksilver=v2` calls retain upstream Live routing, attestation, call storage,
  and duration billing. The root `/realtime` route remains Grok-only.
- Astra behavior remains upstream-owned; these patches add no Astra override.
- Patchset documentation stays on `patchset`. Release generation starts from
  upstream and applies the patches, without copying this page, `README.md`,
  or `RELEASE_POLICY.md` into `main`, `mirror/upstream-main`, or `patched`.

## Applying selected capabilities

Apply patches in numerical order. The complete series contains all 11 patches.
For separate capabilities:

- STT: 3, 4, 5, 6. Patch 3 supplies cached-token compatibility, not audio pricing.
- Realtime WebSocket: 7.
- Realtime transport without moderation: 4, 7, 8.
- Realtime moderation without REST: 7, 9.
- Integrated Realtime: 4, 7, 8, 9, 10.
- Docker publication, pnpm cache, cached-token parsing and caller-prompt
  preservation can be selected independently as patches 1, 2, 3 and 11.

Runtime verification and replay against the automatically selected upstream
run in CI. Patch sizes and the manual review version are documentation only.

## Codex Realtime proxy configuration

Set these top-level keys in Codex `config.toml` when routing WebRTC through
this proxy. Use the proxy API base, without `/realtime/calls` or a call ID:

```toml
experimental_realtime_webrtc_call_base_url = "https://your-proxy.example/v1"
experimental_realtime_ws_base_url = "https://your-proxy.example/v1"
```

The HTTP override is `experimental_realtime_webrtc_call_base_url`; without it,
call creation uses the selected provider base. WebRTC sideband uses a separate
base that defaults to `https://api.openai.com/v1`, so setting only the provider
base does not route that connection through the proxy. Keep the existing Codex
authentication configured with the proxy key; these URL settings do not set it.

For V1, Codex appends `/realtime/calls` to the HTTP base and opens
`wss://your-proxy.example/v1/realtime?intent=quicksilver&call_id=...` for the
sideband. V3 uses `/v1/live` and `/v1/live/<call_id>` with these public API bases;
the proxy retains upstream Live handling for that protocol.

The [official configuration reference](https://developers.openai.com/codex/config-reference/)
documents the WebSocket override. The separate HTTP override and provider
fallback are in [Codex realtime setup](https://github.com/openai/codex/blob/main/codex-rs/core/src/realtime_conversation.rs),
with endpoint construction in [HTTP calls](https://github.com/openai/codex/blob/main/codex-rs/codex-api/src/endpoint/realtime_call.rs)
and [WebSocket methods](https://github.com/openai/codex/blob/main/codex-rs/codex-api/src/endpoint/realtime_websocket/methods.rs).
