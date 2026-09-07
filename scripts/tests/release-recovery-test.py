#!/usr/bin/env python3
"""Exercise release preparation with local Git repositories and publication mocks."""

from __future__ import annotations

import os
import shutil
import subprocess
import tempfile
import textwrap
import unittest
from pathlib import Path


ROOT = Path(__file__).parents[2]
VERSION = "v0.2.1-patch.1"


class ReleaseRecoveryTest(unittest.TestCase):
    def setUp(self) -> None:
        self.temp = tempfile.TemporaryDirectory()
        self.addCleanup(self.temp.cleanup)
        self.path = Path(self.temp.name)
        self.env = dict(os.environ, GIT_CONFIG_GLOBAL=str(self.path / "gitconfig"))
        self.run_command("git", "config", "--global", "user.name", "test")
        self.run_command("git", "config", "--global", "user.email", "test@example.com")
        self.upstream = self.path / "upstream"
        self.run_command("git", "init", "-q", "-b", "main", str(self.upstream))
        self.write("upstream/backend/cmd/server/VERSION", "0.2.1\n")
        self.write("upstream/.github/workflows/ci.yml", "name: upstream\n")
        self.commit(self.upstream, "upstream")
        self.source = self.git(self.upstream, "rev-parse", "HEAD")

        self.downstream = self.path / "downstream"
        self.run_command("git", "clone", "-q", str(self.upstream), str(self.downstream))
        self.git(self.downstream, "rm", "-r", ".github/workflows")
        self.commit(self.downstream, "Remove upstream workflows from patched branch")
        self.patched = self.git(self.downstream, "rev-parse", "HEAD")
        self.git(self.downstream, "tag", VERSION)
        self.git(self.downstream, "branch", "patched")
        self.git(self.downstream, "checkout", "--detach", self.source)
        self.git(self.downstream, "rm", "-r", ".github/workflows")
        self.write(
            "downstream/.github/workflows/upstream-pr-check.yml",
            (ROOT / ".github/workflows/upstream-pr-check.yml").read_text(),
        )
        self.commit(self.downstream, "mirror")
        self.main = self.git(self.downstream, "rev-parse", "HEAD")
        self.git(self.downstream, "branch", "-f", "main", self.main)
        self.git(self.downstream, "branch", "mirror/upstream-main", self.main)
        self.run_command(
            "git", "config", "--global", f"url.{self.downstream}.insteadOf",
            "https://github.com/example/downstream.git",
        )

        for name in (
            ".github/workflows/upstream-pr-check.yml",
            "scripts/release-publication-state.sh",
            "scripts/compute-next-version.sh",
        ):
            self.write(f"sub2api-patch/{name}", (ROOT / name).read_text(), executable=name.endswith(".sh"))
        self.write("sub2api-patch/scripts/apply-patches.sh", "#!/bin/bash\nexit 0\n", executable=True)
        self.write("bin/curl", '#!/bin/bash\nprintf "%s" "$RELEASE_STATUS"\n', executable=True)
        self.write("bin/docker", textwrap.dedent("""\
            #!/bin/bash
            case "$IMAGE_STATE" in
              present) exit 0 ;;
              missing) echo 'manifest unknown' >&2; exit 1 ;;
              *) echo 'registry unavailable' >&2; exit 1 ;;
            esac
            """), executable=True)
        self.write("bin/gh", textwrap.dedent(f"""\
            #!/bin/bash
            case "$2" in
              *matching-refs*) echo refs/tags/{VERSION} ;;
              *) echo {VERSION} ;;
            esac
            """), executable=True)
        workflow = (ROOT / ".github/workflows/auto-release.yml").read_text()
        step = workflow.split("      - name: Prepare mirror and patched commits\n", 1)[1]
        step = step.split("\n      - name:", 1)[0]
        self.write("prepare.sh", textwrap.dedent(step.split("        run: |\n", 1)[1]))
        self.env.update(
            PATH=f"{self.path / 'bin'}:{os.environ['PATH']}",
            UPSTREAM_REPOSITORY="Wei-Shaw/sub2api",
            UPSTREAM_GIT_URL=str(self.upstream),
            UPSTREAM_SOURCE_SHA=self.source,
            EXPECTED_MAIN_SHA=self.main,
            EXPECTED_MIRROR_SHA=self.main,
            EXPECTED_PATCHED_SHA=self.patched,
            GITHUB_REPOSITORY="example/downstream",
            GITHUB_OUTPUT=str(self.path / "outputs"),
            GH_TOKEN="test-token",
            IMAGE="example/image",
            IMAGE_STATE="present",
            RELEASE_STATUS="200",
        )

    def write(self, name: str, content: str, executable: bool = False) -> None:
        path = self.path / name
        path.parent.mkdir(parents=True, exist_ok=True)
        path.write_text(content)
        if executable:
            path.chmod(0o755)

    def run_command(self, *args: str) -> str:
        return subprocess.check_output(args, cwd=self.path, env=self.env, text=True, stderr=subprocess.PIPE).strip()

    def git(self, repo: Path, *args: str) -> str:
        return self.run_command("git", "-C", str(repo), *args)

    def commit(self, repo: Path, message: str) -> None:
        self.git(repo, "add", "-A")
        self.git(repo, "commit", "-qm", message)

    def prepare(self, **env: str) -> subprocess.CompletedProcess[str]:
        shutil.rmtree(self.path / "worktree", ignore_errors=True)
        (self.path / "outputs").unlink(missing_ok=True)
        return subprocess.run(
            ["bash", "prepare.sh"], cwd=self.path, env=dict(self.env, **env),
            text=True, capture_output=True,
        )

    def outputs(self) -> dict[str, str]:
        return dict(line.split("=", 1) for line in (self.path / "outputs").read_text().splitlines())

    def test_missing_outputs_resume_the_existing_tag(self) -> None:
        for image, release in (("missing", "404"), ("present", "404"), ("missing", "200")):
            with self.subTest(image=image, release=release):
                result = self.prepare(IMAGE_STATE=image, RELEASE_STATUS=release)
                self.assertEqual(result.returncode, 0, result.stderr)
                output = self.outputs()
                self.assertEqual(output["version"], VERSION)
                self.assertEqual(output["resume"], "true")
                self.assertEqual(output["publish_image"], str(image == "missing").lower())
                self.assertEqual(output["publish_release"], str(release == "404").lower())
                self.assertEqual(self.git(self.path / "worktree", "rev-parse", "HEAD"), self.patched)
                self.assertEqual(self.git(self.downstream, "rev-parse", VERSION), self.patched)

    def test_complete_publication_and_matching_trees_are_noop(self) -> None:
        result = self.prepare()
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertEqual(self.outputs(), {"changed": "false"})

    def test_recovery_keeps_reserved_source_when_upstream_advances(self) -> None:
        self.write("upstream/backend/cmd/server/VERSION", "0.2.2\n")
        self.commit(self.upstream, "next upstream")
        selected = self.git(self.upstream, "rev-parse", "HEAD")
        result = self.prepare(UPSTREAM_SOURCE_SHA=selected, RELEASE_STATUS="404")
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertEqual(self.outputs()["upstream_source_sha"], self.source)
        self.assertEqual(self.git(self.path / "worktree", "rev-parse", "HEAD"), self.patched)
        self.assertIn("selected content will be evaluated on the next sync", result.stdout)

    def test_mirror_drift_requires_publication(self) -> None:
        result = self.prepare(EXPECTED_MIRROR_SHA=self.source)
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertEqual(self.outputs()["resume"], "false")
        self.assertEqual(self.outputs()["version"], "v0.2.1-patch.2")

    def test_exact_annotated_tag_updates_release_name_without_source_changes(self) -> None:
        self.git(self.upstream, "tag", "-a", "v0.2.2", "-m", "release", self.source)
        result = self.prepare()
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertEqual(self.outputs()["version"], "v0.2.2-patch.1")
        self.assertEqual(self.outputs()["resume"], "false")
        self.assertEqual(
            self.git(self.path / "worktree", "rev-parse", "HEAD^{tree}"),
            self.git(self.downstream, "rev-parse", f"{self.patched}^{{tree}}"),
        )

    def test_release_tag_on_other_commit_does_not_change_selected_version(self) -> None:
        self.write("upstream/backend/cmd/server/VERSION", "0.2.2\n")
        self.commit(self.upstream, "next upstream")
        self.git(self.upstream, "tag", "-a", "v0.2.2", "-m", "release")
        result = self.prepare()
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertEqual(self.outputs(), {"changed": "false"})

    def test_lookup_failures_do_not_become_noop(self) -> None:
        for env in ({"IMAGE_STATE": "error"}, {"RELEASE_STATUS": "503"}):
            with self.subTest(env=env):
                result = self.prepare(**env)
                self.assertNotEqual(result.returncode, 0)
                self.assertFalse((self.path / "outputs").exists())


if __name__ == "__main__":
    unittest.main()
