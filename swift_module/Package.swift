// swift-tools-version: 5.9
import PackageDescription

// Compile the exact platform-neutral source that is also part of the
// production CommonDI target. This catches drift without requiring signing,
// NetworkExtension, or an iOS device.
let package = Package(
    name: "DobbyVPNIOSLifecycleCore",
    // The lifecycle-core target uses Foundation/Swift APIs available on
    // macOS 12. Keep the package floor aligned with the shipped desktop
    // binaries instead of requiring a newer host just to run these tests.
    platforms: [.macOS(.v12)],
    products: [
        .library(name: "IOSLifecycleCore", targets: ["IOSLifecycleCore"]),
    ],
    targets: [
        .target(
            name: "IOSLifecycleCore",
            path: "CommonDI",
            exclude: [
                "CommonDI.h",
                "ExportLogsInteractorImpl.swift",
                "DobbyConfigsRepositoryImpl.swift",
                "IOSSessionShell.swift",
                "SharedKeychainSecretStore.swift",
                "VpnManagerImpl.swift",
                "AppCompositionRoot.swift",
            ],
            sources: [
                "IOSProviderMessageProtocol.swift",
            ]
        ),
        .testTarget(
            name: "IOSLifecycleCoreTests",
            dependencies: ["IOSLifecycleCore"],
            path: "Tests/IOSLifecycleCoreTests"
        ),
    ]
)
