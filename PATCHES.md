# Patchset

The current historical downstream delta is stored as one replayable baseline patch:

- `patches/cur/0001-baseline-downstream-current-dev.patch`

The baseline is intentionally squashed because the original downstream history contains older commits whose individual bases predate the latest upstream sync. Replaying those historical commits one by one conflicts even though the final downstream tree is valid.

Future downstream work should be added as separate logical patches after applying this baseline.

Current baseline topics include:

- CI image publishing
- IPv6-only proxy probing
- OpenAI realtime websocket proxying
- OpenAI audio transcription proxying
- Audio usage accounting and billing
- OpenAI realtime REST endpoints

Patch subjects and generated release notes intentionally avoid pull request and issue references.
