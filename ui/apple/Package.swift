// swift-tools-version: 5.9
import PackageDescription

// This manifest overlays the product manifest only inside a disposable source
// tree. Every non-test target points at production sources in that tree.
let package = Package(
    name: "DobbyVPNIOSLifecycleCore",
    platforms: [.macOS(.v12)],
    products: [
        .library(name: "IOSLifecycleCore", targets: ["IOSLifecycleCore"]),
        .library(name: "DobbyNativeUI", targets: ["DobbyNativeUI"]),
        .executable(name: "DobbyVPNMacApp", targets: ["DobbyVPNMacApp"]),
    ],
    targets: [
        .target(name: "DobbyDiagnosticFiles", path: "shared/diagnostics", linkerSettings: [.linkedLibrary("z")]),
        .target(
            name: "DobbyNativeUI",
            dependencies: ["DobbyDiagnosticFiles"],
            path: "shared/ui"
        ),
        .testTarget(
            name: "DobbyNativeUITests",
            dependencies: ["DobbyNativeUI", "DobbyDiagnosticFiles"],
            path: "shared/tests"
        ),
        .executableTarget(
            name: "DobbyVPNMacApp",
            dependencies: ["DobbyNativeUI", "DobbyDiagnosticFiles"],
            path: "macos/app"
        ),
        .testTarget(
            name: "DobbyMacTransportTests",
            dependencies: ["DobbyVPNMacApp"],
            path: "macos/tests",
            exclude: ["UITests"]
        ),
        .target(
            name: "IOSLifecycleCore",
            dependencies: ["DobbyDiagnosticFiles"],
            path: "ios/integration",
            exclude: [
                "IOSIntegration.h",
                "DobbyConfigsRepositoryImpl.swift",
                "IOSSessionShell.swift",
                "SharedKeychainSecretStore.swift",
                "VpnManagerImpl.swift",
                "VpnManagerPreferences.swift",
                "AppCompositionRoot.swift",
            ],
            sources: [
                "IOSProviderMessageProtocol.swift",
                "IOSProviderOperations.swift",
                "LogStore.swift",
                "IOSProviderStopBoundary.swift",
                "VPNManagerIdentity.swift",
                "VPNPreferenceCoordinator.swift",
                "IOSLifecycleDiagnostics.swift",
                "IOSReadinessFailureCooldown.swift",
                "TunnelSettingsOwner.swift",
            ]
        ),
        .testTarget(
            name: "IOSLifecycleCoreTests",
            dependencies: ["IOSLifecycleCore", "DobbyDiagnosticFiles"],
            path: "ios/lifecycle_tests"
        ),
        .testTarget(
            name: "IOSNativeDeadlineSupportTests",
            path: "ios/deadline_support",
            sources: ["NativeProcessDeadline.swift", "NativeProcessDeadlineTests.swift"]
        ),
    ]
)
