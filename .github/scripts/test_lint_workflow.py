from pathlib import Path
import re
import unittest


ROOT = Path(__file__).resolve().parents[2]


class GoLintWorkflowTests(unittest.TestCase):
    @classmethod
    def setUpClass(cls) -> None:
        cls.workflow = (ROOT / ".github/workflows/lint.yml").read_text(encoding="utf-8")

    def test_go_lint_stages_the_shared_native_dependency_closure(self) -> None:
        helper = "python3 .github/scripts/desktop_build.py prepare-go-test-deps --go-mod-tidy"
        self.assertIn(helper, self.workflow)
        self.assertLess(
            self.workflow.index(helper),
            self.workflow.index("Install pinned golangci-lint"),
        )

    def test_go_lint_uses_the_pinned_version(self) -> None:
        self.assertIn(
            "go install github.com/golangci/golangci-lint/v2/cmd/golangci-lint@v2.13.2",
            self.workflow,
        )

    def test_reviewdog_cannot_mask_the_authoritative_linter_status(self) -> None:
        self.assertIn("-fail-level=none", self.workflow)
        self.assertIn("if golangci-lint run", self.workflow)
        self.assertIn("lint_status=0", self.workflow)
        self.assertRegex(self.workflow, re.compile(r"lint_status=\$\?"))
        self.assertIn('exit "$lint_status"', self.workflow)
        self.assertIn("|| true", self.workflow)


if __name__ == "__main__":
    unittest.main()
