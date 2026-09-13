from pathlib import Path
import unittest


SOURCE = (
    Path(__file__).resolve().parents[2]
    / "kmp_module/app/src/iosMain/kotlin/com/dobby/feature/logging/domain/LogsRepository.ios.kt"
)


class IosLogStorageContractTests(unittest.TestCase):
    def test_mini_mode_keeps_initialization_and_is_simulator_only(self) -> None:
        root = Path(__file__).resolve().parents[2]
        source = (root / "swift_module/iosApp/iOSApp.swift").read_text()
        composition_root = (root / "swift_module/CommonDI/AppCompositionRoot.swift").read_text()
        content_view = (root / "swift_module/iosApp/ContentView.swift").read_text()
        self.assertIn("IOSAppCompositionRoot.logsRepository", source)
        self.assertIn("createIosAppDependencies", composition_root)
        self.assertIn("IOSAppCompositionRoot.appDependencies", content_view)
        self.assertEqual(source.count("#if DOBBY_SIMULATOR_MINI && targetEnvironment(simulator)"), 2)
        self.assertIn('startup.initialized mode=mini', source)
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
