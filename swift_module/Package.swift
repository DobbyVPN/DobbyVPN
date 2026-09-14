// swift-tools-version: 5.9
import PackageDescription

// Compile the exact platform-neutral source that is also part of the
// production CommonDI target. This catches drift without requiring signing,
// generated KMP frameworks, NetworkExtension, or an iOS device.
let package = Package(
    name: "DobbyVPNIOSLifecycleCore",
    platforms: [.macOS(.v13)],
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
