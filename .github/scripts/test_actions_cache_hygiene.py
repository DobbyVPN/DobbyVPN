from pathlib import Path
import re
import unittest


ROOT = Path(__file__).resolve().parents[2]
WORKFLOWS = ROOT / ".github/workflows"


class ActionsCacheHygieneTests(unittest.TestCase):
    @classmethod
    def setUpClass(cls) -> None:
        cls.workflows = {
            path: path.read_text(encoding="utf-8")
            for path in sorted(WORKFLOWS.glob("*.yml"))
        }
        cls.combined = "\n".join(cls.workflows.values())

    def test_workflows_do_not_use_persistent_actions_caches(self) -> None:
        self.assertNotIn("uses: actions/cache@", self.combined)
        self.assertNotIn("cache-dependency-path:", self.combined)
        self.assertNotIn("cache-hit", self.combined)

    def test_every_setup_go_disables_module_cache(self) -> None:
        setup_go_count = len(re.findall(r"uses: actions/setup-go@", self.combined))
        disabled_count = len(
            re.findall(r"^\s+cache: false\s*$", self.combined, re.MULTILINE)
        )
        self.assertGreater(setup_go_count, 0)
        self.assertEqual(setup_go_count, disabled_count)

    def test_java_and_ruby_setup_do_not_enable_dependency_caches(self) -> None:
        self.assertNotRegex(self.combined, r"^\s+cache:\s+gradle\s*$")
        self.assertNotRegex(self.combined, r"^\s+bundler-cache:\s+true\s*$")
        self.assertNotIn("cache-disabled: false", self.combined)

    def test_gradle_setup_disables_gradle_user_home_cache(self) -> None:
        setup_gradle_count = len(
            re.findall(r"uses: gradle/actions/setup-gradle@", self.combined)
        )
        disabled_count = len(
            re.findall(r"^\s+cache-disabled: true\s*$", self.combined, re.MULTILINE)
        )
        self.assertEqual(setup_gradle_count, disabled_count)


if __name__ == "__main__":
    unittest.main()
