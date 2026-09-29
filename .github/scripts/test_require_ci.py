import unittest

from require_ci import REQUIRED_CI_JOB_NAMES, missing_or_failed_required_jobs


def successful_jobs():
    return {
        "jobs": [
            {"name": name, "status": "completed", "conclusion": "success"}
            for name in REQUIRED_CI_JOB_NAMES
        ]
    }


class RequireCITests(unittest.TestCase):
    def test_all_required_jobs_must_succeed(self):
        self.assertEqual(missing_or_failed_required_jobs(successful_jobs()), [])

    def test_missing_complete_marker_rejects_focused_run(self):
        payload = successful_jobs()
        payload["jobs"] = [job for job in payload["jobs"] if job["name"] != "Complete CI"]

        self.assertEqual(missing_or_failed_required_jobs(payload), ["Complete CI"])

    def test_skipped_or_failed_job_is_rejected(self):
        payload = successful_jobs()
        payload["jobs"][0]["conclusion"] = "skipped"

        self.assertEqual(
            missing_or_failed_required_jobs(payload),
            [REQUIRED_CI_JOB_NAMES[0]],
        )

    def test_duplicate_required_job_with_failure_is_rejected(self):
        payload = successful_jobs()
        payload["jobs"].append(
            {
                "name": REQUIRED_CI_JOB_NAMES[0],
                "status": "completed",
                "conclusion": "failure",
            }
        )

        self.assertEqual(
            missing_or_failed_required_jobs(payload),
            [REQUIRED_CI_JOB_NAMES[0]],
        )

    def test_invalid_jobs_payload_is_rejected(self):
        with self.assertRaisesRegex(ValueError, "job list is invalid"):
            missing_or_failed_required_jobs({"jobs": None})


if __name__ == "__main__":
    unittest.main()
