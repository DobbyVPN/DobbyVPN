// swift-tools-version: 5.9
import PackageDescription

// Compile the platform-neutral protocol and lifecycle-diagnostic sources that
// are also used by the production IOSIntegration target. This catches drift without
// requiring signing, NetworkExtension, or an iOS device.
let package = Package(
    name: "DobbyVPNIOSLifecycleCore",
    // The lifecycle-core target uses Foundation/Swift APIs available on
    // macOS 12. Keep the package floor aligned with the shipped desktop
    // binaries instead of requiring a newer host just to run these tests.
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
            dependencies: ["DobbyNativeUI"],
            path: "macos/app"
        ),
        .testTarget(
            name: "DobbyMacTransportTests",
            dependencies: ["DobbyVPNMacApp"],
            path: "macos/tests"
        ),
        .target(
            name: "IOSLifecycleCore",
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
                "IOSProviderStopBoundary.swift",
                "IOSLifecycleDiagnostics.swift",
                "IOSReadinessFailureCooldown.swift",
                "TunnelSettingsOwner.swift",
            ]
        ),
        .testTarget(
            name: "IOSLifecycleCoreTests",
            dependencies: ["IOSLifecycleCore"],
            path: "ios/lifecycle_tests"
        ),
    ]
)
