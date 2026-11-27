#!/usr/bin/env python3
"""Smoke-test the release workflow's functional wiring."""

from __future__ import annotations

import re
import subprocess
import tempfile
import unittest
from pathlib import Path


ROOT = Path(__file__).parents[2]
SYNC = (ROOT / ".github/workflows/sync-upstream.yml").read_text()
VALIDATION = (ROOT / ".github/workflows/upstream-pr-check.yml").read_text()
RELEASE = (ROOT / ".github/workflows/auto-release.yml").read_text()


class WorkflowPolicyTest(unittest.TestCase):
    def test_gemini_idle_helper_owns_virtual_time(self) -> None:
        patch = next((ROOT / 'patches/cur').glob('0015-*.patch')).read_text()
        self.assertIn('+\t\tout = runAntigravityGeminiStreamWithIdleInBubble(t, userAgent, idle)', patch)
        self.assertEqual(patch.count('+\tsynctest.Test(t,'), 1)

    def test_quota_gateway_wiring_targets_account_usage(self) -> None:
        patch = next((ROOT / 'patches/cur').glob('0024-*.patch')).read_text()
        hunk = next(h for h in re.findall(r'(?ms)^@@ .*?(?=^@@ |^diff --git |\Z)', patch)
                    if '+\tservice.openaiGatewayService = openAIGatewayService' in h)
        self.assertIn(' \tif opsService != nil {', hunk)
        relative = 'backend/internal/service/wire.go'
        source = ('func ProvideOpenAIQuotaService() {\n'
                  '\tservice.agentIdentityWS = openAIGatewayService\n\treturn service\n}\n'
                  'func ProvideAccountUsageService() {\n'
                  '\tservice.agentIdentityWS = openAIGatewayService\n'
                  '\tif opsService != nil {\n\t\tservice.opsMonitoring = opsService\n\t}\n}\n')
        with tempfile.TemporaryDirectory() as directory:
            target = Path(directory) / relative
            target.parent.mkdir(parents=True)
            target.write_text(source)
            subprocess.run(['git', 'init', '-q', directory], check=True)
            result = subprocess.run(['git', 'apply', '-'], input=f'--- a/{relative}\n+++ b/{relative}\n{hunk}', cwd=directory, text=True, capture_output=True)
            self.assertEqual(result.returncode, 0, result.stderr)
            quota, usage = target.read_text().split('func ProvideAccountUsageService()', 1)
            self.assertNotIn('service.openaiGatewayService =', quota)
            self.assertIn('service.openaiGatewayService = openAIGatewayService', usage)

    def test_account_creation_hunk_has_unique_commit_boundary(self) -> None:
        patch = next((ROOT / 'patches/cur').glob('0017-*.patch')).read_text()
        hunk = next(h for h in re.findall(r'(?ms)^@@ .*?(?=^@@ |^diff --git |\Z)', patch)
                    if '+\tif err := createAccountWithGroups(ctx, s.accountRepo, account, groupIDs)' in h)
        self.assertIn('AfterAccountCommit(ctx, func(_ context.Context)', hunk)

    def test_codex_import_preserves_upstream_group_sync_button(self) -> None:
        patch = next((ROOT / 'patches/cur').glob('0017-*.patch')).read_text()
        for name in ['CreateAccountModal', 'EditAccountModal']:
            relative = f'frontend/src/components/account/{name}.vue'
            diff = patch.split(f'diff --git a/{relative} b/{relative}\n', 1)[1].split('\ndiff --git ', 1)[0]
            hunk = next(h for h in re.findall(r'(?ms)^@@ .*?(?=^@@ |\Z)', diff)
                        if '+import CodexAPICredentialsFields' in h)
            source = "import UpstreamRequestIdHeaderField from '@/components/account/UpstreamRequestIdHeaderField.vue'\nimport UpstreamGroupSyncButton from '@/components/account/UpstreamGroupSyncButton.vue'\n"
            with tempfile.TemporaryDirectory() as directory:
                target = Path(directory) / relative
                target.parent.mkdir(parents=True)
                target.write_text(source)
                subprocess.run(['git', 'init', '-q', directory], check=True)
                result = subprocess.run(['git', 'apply', '-'], input=f'--- a/{relative}\n+++ b/{relative}\n{hunk}', cwd=directory, text=True, capture_output=True)
                self.assertEqual(result.returncode, 0, result.stderr)
                self.assertIn('import CodexAPICredentialsFields', target.read_text())
                self.assertIn('import UpstreamGroupSyncButton', target.read_text())

    def test_codex_dispatch_preserves_conflicting_fields_guard(self) -> None:
        patch = next((ROOT / 'patches/cur').glob('0017-*.patch')).read_text()
        lines = [line for line in patch.splitlines()
                 if 'groupModelAllowlist, compositeTarget' in line
                 and ('r.Handle(' in line or 'codexDirect' in line)]
        self.assertEqual(lines, [], 'Route patches must retain rejectConflictingFields')

    def test_quota_import_accepts_account_usage_refresh_key(self) -> None:
        patch = next((ROOT / 'patches/cur').glob('0024-*.patch')).read_text()
        hunk = next(h for h in re.findall(r'(?ms)^@@ .*?(?=^@@ |^diff --git |\Z)', patch)
                    if '+import type { OpenAIRateLimitWindow }' in h)
        relative = 'frontend/src/components/account/AccountUsageCell.vue'
        source = "import type { Account, AccountUsageInfo, GeminiCredentials, WindowStats } from '@/types'\nimport { buildAccountUsageRefreshKey } from '@/utils/accountUsageRefresh'\n"
        with tempfile.TemporaryDirectory() as directory:
            target = Path(directory) / relative
            target.parent.mkdir(parents=True)
            target.write_text(source)
            subprocess.run(['git', 'init', '-q', directory], check=True)
            result = subprocess.run(['git', 'apply', '-'],
                                    input=f'--- a/{relative}\n+++ b/{relative}\n{hunk}',
                                    cwd=directory, text=True, capture_output=True)
            self.assertEqual(result.returncode, 0, result.stderr)
            self.assertIn('buildAccountUsageRefreshKey', target.read_text())
            self.assertIn('import type { OpenAIRateLimitWindow }', target.read_text())

    def test_gpt62_patch_applies_after_upstream_import(self) -> None:
        patch = next((ROOT / 'patches/cur').glob('0025-*.patch'))
        with tempfile.TemporaryDirectory() as directory:
            subprocess.run(['git', 'init', '-q', directory], check=True)
            result = subprocess.run(['git', 'apply', str(patch)], cwd=directory,
                                    text=True, capture_output=True)
            self.assertEqual(result.returncode, 0, result.stderr)
            source = (Path(directory) / 'backend/internal/service/openai_gpt62_downstream_test.go').read_text()
            for expected in ['gpt-6.2-sol', 'gpt-6.2-astra', 'CacheCreation12h', 'CalculateCostUnified']:
                self.assertIn(expected, source)

    def test_codex_account_patch_preserves_upstream_passthrough_selector(self) -> None:
        patch = next((ROOT / 'patches/cur').glob('0017-*.patch')).read_text()
        relative = 'frontend/src/components/account/CreateAccountModal.vue'
        file_diff = patch.split(f'diff --git a/{relative} b/{relative}\n', 1)[1]
        file_diff = file_diff.split('\ndiff --git ', 1)[0]
        hunks = re.findall(r'(?ms)^@@ .*?(?=^@@ |\Z)', file_diff)
        selector_hunks = [hunk for hunk in hunks if 'create-openai-passthrough-toggle' in hunk]
        focused_patch = (f'--- a/{relative}\n+++ b/{relative}\n' + ''.join(selector_hunks)
                         if selector_hunks else '')
        source = '''          <button
            type="button"
            data-testid="create-openai-passthrough-toggle"
            @click="openaiPassthroughEnabled = !openaiPassthroughEnabled"
          >
'''
        with tempfile.TemporaryDirectory() as directory:
            target = Path(directory) / relative
            target.parent.mkdir(parents=True)
            target.write_text(source)
            subprocess.run(['git', 'init', '-q', directory], check=True)
            result = subprocess.run(['git', 'apply', '--allow-empty', '-'], input=focused_patch,
                                    cwd=directory, text=True, capture_output=True)
            self.assertEqual(result.returncode, 0, result.stderr)
            self.assertEqual(target.read_text(), source)
        self.assertIn('form.platform === \'openai\' && form.type !== \'codex-api\'', file_diff)

    def test_realtime_patch_preserves_upstream_client_session_identity(self) -> None:
        patch = next((ROOT / 'patches/cur').glob('0007-*.patch')).read_text()
        relative = 'backend/internal/service/openai_ws_v2_passthrough_adapter.go'
        file_diff = patch.split(f'diff --git a/{relative} b/{relative}\n', 1)[1]
        file_diff = file_diff.split('\ndiff --git ', 1)[0]
        hunks = re.findall(r'(?ms)^@@ .*?(?=^@@ |\Z)', file_diff)
        identity_hunks = [hunk for hunk in hunks
                          if 'applyCodexAccountIdentityClientMetadataRaw' in hunk]
        self.assertEqual(len(identity_hunks), 1)
        # Exercise the conflicting hunk against an independent upstream fixture.
        focused_patch = f'--- a/{relative}\n+++ b/{relative}\n' + identity_hunks[0]
        with tempfile.TemporaryDirectory() as directory:
            target = Path(directory) / relative
            target.parent.mkdir(parents=True)
            target.write_text((ROOT / 'scripts/tests/fixtures/upstream-ws-client-identity.go').read_text())
            subprocess.run(['git', 'init', '-q', directory], check=True)
            result = subprocess.run(['git', 'apply', '-'], input=focused_patch,
                                    cwd=directory, text=True, capture_output=True)
            self.assertEqual(result.returncode, 0, result.stderr)
            source = target.read_text()
            self.assertIn('if !isRealtime {\n\t\taccountScopedFirst', source)
            self.assertIn('getAPIKeyIDFromContext(c), codexClientSessionID(c))', source)
            self.assertIn('firstClientMessage = accountScopedFirst', source)
            self.assertIn('"invalid websocket identity metadata", scopeErr)', source)

    def test_audio_patch_preserves_antigravity_scopes(self) -> None:
        patch = next((ROOT / 'patches/cur').glob('0005-*.patch'))
        relative = 'backend/internal/service/model_rate_limit.go'
        with tempfile.TemporaryDirectory() as directory:
            target = Path(directory) / relative
            target.parent.mkdir(parents=True)
            target.write_text((ROOT / 'scripts/tests/fixtures/upstream-model-rate-limit.go').read_text())
            subprocess.run(['git', 'init', '-q', directory], check=True)
            subprocess.run(['git', 'apply', '--include=' + relative, str(patch)],
                           cwd=directory, check=True, capture_output=True)
            source = target.read_text()
            self.assertIn('keys = antigravityModelRateLimitKeys(a, modelKey)', source)
            self.assertNotIn('return antigravityModelRateLimitKeys(a, modelKey)', source)
            function = source.split('func (a *Account) modelRateLimitKeysForRequest', 1)[1]
            self.assertLess(function.index('keys = antigravityModelRateLimitKeys'),
                            function.index('for _, scope := range modelRateLimitExtraScopesFromContext'))
            self.assertIn('keys = appendModelRateLimitKey(keys, scope)', function)

    def test_endpoint_body_errors_use_upstream_classifier(self) -> None:
        for prefix in ('0006-', '0008-'):
            patch = next((ROOT / 'patches/cur').glob(prefix + '*.patch')).read_text()
            self.assertNotIn('extractMaxBytesError', patch)
            self.assertIn('requestBodyReadFailure(reqLog, c.Request, err)', patch)

    def test_subscription_patch_accepts_upstream_per_turn_token_timing(self) -> None:
        patch = ROOT / 'patches/cur/0020-feat-forward-Codex-API-subscription-inference-endpoi.patch'
        relay = 'backend/internal/service/openai_ws_v2/passthrough_relay.go'
        # Upstream owns this implementation; replay must retain it unchanged.
        source = '''package openai_ws_v2
func observeUpstreamMessage() {
    turnTiming := state.activeTurn
    if turnTiming != nil && turnTiming.firstTokenMs == nil && isTokenEvent(eventType) {
        ms := int(now.Sub(turnTiming.startAt).Milliseconds())
        if ms >= 0 {
            turnTiming.firstTokenMs = &ms
        }
    }
}
'''
        with tempfile.TemporaryDirectory() as directory:
            target = Path(directory) / relay
            target.parent.mkdir(parents=True)
            target.write_text(source)
            subprocess.run(['git', 'init', '-q', directory], check=True)
            subprocess.run(['git', 'apply', '--include=' + relay, str(patch)],
                           cwd=directory, check=True, capture_output=True)
            self.assertEqual(target.read_text(), source)
        self.assertIn('TestObserveUpstreamMessage_FirstTokenWithoutResponseIDEachTurn',
                      patch.read_text())

    def test_patchset_push_and_sync_triggers_exist(self) -> None:
        self.assertRegex(VALIDATION, r"(?m)^  push:\n    branches:\n      - patchset$")
        self.assertRegex(SYNC, r"(?m)^  schedule:$")
        self.assertRegex(SYNC, r"(?m)^  workflow_dispatch:$")
        self.assertNotIn("workflow_dispatch:", RELEASE)

    def test_sync_resolves_current_eligible_upstream(self) -> None:
        self.assertIn("refs/remotes/upstream/main", SYNC)
        self.assertIn('upstream-ci-ready.sh "$UPSTREAM_REPOSITORY" "$upstream_head"', SYNC)
        self.assertNotRegex(SYNC, r"upstream_(?:ref|sha):\s*[0-9a-f]{40}")
        readiness = (ROOT / "scripts/upstream-ci-ready.sh").read_text()
        self.assertIn("test", readiness)
        self.assertIn("frontend", readiness)
        self.assertIn("golangci-lint", readiness)

    def test_release_depends_on_validation(self) -> None:
        release_job = SYNC.split("\n  release:\n", 1)[1]
        self.assertRegex(release_job, r"needs:\n      - resolve\n      - validate")
        self.assertIn("needs.validate.result == 'success'", release_job)
        for gate in (
            "Apply patches",
            "Backend tests",
            "Backend lint and format checks",
            "Frontend checks",
            "Preflight patched image build",
        ):
            self.assertIn(f"- name: {gate}", VALIDATION)

    def test_linter_supports_go_1_27_2_export_data(self) -> None:
        lint_step = VALIDATION.split("- name: Backend lint and format checks\n", 1)[1]
        lint_step = lint_step.split("\n      - name:", 1)[0]
        self.assertIn("version: v2.14.0", lint_step)

    def test_backend_containers_use_docker_hub_mirror(self) -> None:
        test_step = VALIDATION.split("- name: Backend tests\n", 1)[1]
        test_step = test_step.split("\n      - name:", 1)[0]
        self.assertIn("TESTCONTAINERS_HUB_IMAGE_NAME_PREFIX: mirror.gcr.io", test_step)
        self.assertIn("make test-integration", test_step)

    def test_validation_and_release_builders_use_docker_hub_mirror(self) -> None:
        for workflow in (VALIDATION, RELEASE):
            buildx_step = workflow.split("- name: Set up Docker Buildx\n", 1)[1]
            buildx_step = buildx_step.split("\n      - name:", 1)[0]
            self.assertIn("image=mirror.gcr.io/moby/buildkit:buildx-stable-1", buildx_step)
            self.assertIn('[registry."docker.io"]', buildx_step)
            self.assertIn('mirrors = ["mirror.gcr.io"]', buildx_step)

    def test_collision_and_compare_and_swap_guards_exist(self) -> None:
        self.assertIn("version tag already exists", RELEASE)
        self.assertIn("GitHub release already exists", RELEASE)
        self.assertIn("versioned image tag already exists", RELEASE)
        self.assertIn("--atomic", RELEASE)
        self.assertEqual(RELEASE.count("--force-with-lease="), 3)
        self.assertIn(":latest-patch", RELEASE)

    def test_upstream_is_read_only(self) -> None:
        combined = SYNC + VALIDATION + RELEASE
        self.assertNotRegex(combined, r"git(?: -C \S+)? push upstream")
        self.assertNotRegex(
            combined,
            r"gh (?:api|pr|issue|release).*Wei-Shaw/sub2api",
        )
        self.assertNotIn("github.com/Wei-Shaw/sub2api.git@", combined)


if __name__ == "__main__":
    unittest.main()
