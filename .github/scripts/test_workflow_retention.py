from pathlib import Path
import unittest


ROOT = Path(__file__).resolve().parents[1] / "workflows"


class WorkflowRetentionTests(unittest.TestCase):
    def read(self, name: str) -> str:
        return (ROOT / name).read_text(encoding="utf-8")

    def test_only_ci_has_direct_source_check_triggers(self):
        ci = self.read("ci.yml")
        self.assertIn("  push:", ci)
        self.assertIn("  pull_request:", ci)
        self.assertIn("  workflow_dispatch:", ci)
        for name in ("test.yml", "lint.yml", "security.yml", "actionlint.yml"):
            text = self.read(name)
            self.assertIn("  workflow_call:", text)
            self.assertNotIn("  pull_request:", text)
            self.assertNotIn("  workflow_dispatch:", text)

    def test_trusted_workflows_share_non_cancelling_group_and_cleanup(self):
        for name in ("ci.yml", "release.yml", "publish.yml"):
            text = self.read(name)
            self.assertIn("group: dobbyvpn-actions-retention", text)
            self.assertIn("cancel-in-progress: false", text)
            self.assertNotIn("queue:", text)
            self.assertIn("actions_retention.py", text)
            self.assertIn("--delete-intermediate-artifacts", text)

    def test_security_does_not_persist_trivy_cache(self):
        text = self.read("security.yml")
        self.assertEqual(text.count("cache: 'false'"), 2)

    def test_publish_requires_newest_completed_run(self):
        text = self.read("publish.yml")
        self.assertIn("latest_completed_run", text)
        self.assertIn('test "$latest_completed_run" = "$RELEASE_RUN_ID"', text)


if __name__ == "__main__":
    unittest.main()
