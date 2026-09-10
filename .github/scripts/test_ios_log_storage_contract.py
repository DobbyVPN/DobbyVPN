from pathlib import Path
import unittest


SOURCE = (
    Path(__file__).resolve().parents[2]
    / "kmp_module/app/src/iosMain/kotlin/com/dobby/feature/logging/domain/LogsRepository.ios.kt"
)


class IosLogStorageContractTests(unittest.TestCase):
    def test_startup_only_mode_keeps_initialization_and_is_simulator_only(self) -> None:
        source = (Path(__file__).resolve().parents[2] / "swift_module/iosApp/iOSApp.swift").read_text()
        self.assertLess(source.index("StartDIKt.startDI"), source.index("startup.initialized"))
        self.assertEqual(source.count("#if DOBBY_STARTUP_TEST && targetEnvironment(simulator)"), 2)
        self.assertIn('startup.initialized mode=startup-only', source)
        self.assertIn('startup.ui_attached mode=normal', source)
        self.assertIn("#else\n            ContentView()", source)

    def test_logs_use_the_app_group_container_without_permission_guards(self) -> None:
        source = SOURCE.read_text(encoding="utf-8")
        self.assertIn('return "$containerPath/$name".toPath()', source)
        self.assertNotIn("chmod", source)
        self.assertNotIn("DobbyVPNLogs", source)
        self.assertIn("failure.printStackTrace()", source)
        self.assertNotIn("runCatching", source)
        self.assertNotIn('error("Failed to get shared log container")', source)


if __name__ == "__main__":
    unittest.main()
