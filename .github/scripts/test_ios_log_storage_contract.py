from pathlib import Path
import unittest


SOURCE = (
    Path(__file__).resolve().parents[2]
    / "kmp_module/app/src/iosMain/kotlin/com/dobby/feature/logging/domain/LogsRepository.ios.kt"
)


class IosLogStorageContractTests(unittest.TestCase):
    def test_permissions_are_applied_to_an_app_owned_child_directory(self) -> None:
        source = SOURCE.read_text(encoding="utf-8")
        self.assertIn('private const val privateLogDirectoryName = "DobbyVPNLogs"', source)
        self.assertIn('return "$containerPath/$privateLogDirectoryName/$name".toPath()', source)
        self.assertIn("chmod(logFilePath.parent.toString(), 448.convert())", source)
        self.assertNotIn('return "$containerPath/$name".toPath()', source)
        self.assertIn("logStorageInitializationAvailable = runCatching", source)
        self.assertNotIn('error("Failed to get shared log container")', source)
        self.assertNotIn('check(chmod(logFilePath.parent', source)


if __name__ == "__main__":
    unittest.main()
