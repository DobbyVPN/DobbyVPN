import unittest

import actions_retention


class ActionsRetentionTests(unittest.TestCase):
    def test_completed_current_and_active_runs_are_selected_correctly(self):
        runs = [
            {"id": 10, "status": "completed"},
            {"id": 11, "status": "in_progress"},
            {"id": 12, "status": "queued"},
            {"id": 13, "status": "completed"},
            {"id": 14, "status": "completed"},
        ]
        self.assertEqual(
            actions_retention.deletion_candidates(runs, "13"),
            [
                actions_retention.Deletion("run", "10"),
                actions_retention.Deletion("run", "14"),
            ],
        )

    def test_caches_are_all_candidates(self):
        self.assertEqual(
            actions_retention.cache_candidates([{"id": 1}, {"id": 2}, {"name": "invalid"}]),
            [actions_retention.Deletion("cache", "1"), actions_retention.Deletion("cache", "2")],
        )

    def test_only_current_run_intermediate_artifacts_are_selected(self):
        artifacts = [
            {"id": 1, "name": "intermediate", "expired": False, "workflow_run": {"id": 9}},
            {"id": 2, "name": "release-package", "expired": False, "workflow_run": {"id": 9}},
            {"id": 3, "name": "old", "expired": False, "workflow_run": {"id": 8}},
        ]
        self.assertEqual(
            actions_retention.artifact_candidates(artifacts, "9", {"release-package"}),
            [actions_retention.Deletion("artifact", "1")],
        )

    def test_pages_are_flattened(self):
        self.assertEqual(
            actions_retention._flatten_runs([
                {"workflow_runs": [{"id": 1}]},
                {"workflow_runs": [{"id": 2}]},
            ]),
            [{"id": 1}, {"id": 2}],
        )


if __name__ == "__main__":
    unittest.main()
