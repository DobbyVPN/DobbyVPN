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
                "AuthenticationManagerImpl.swift",
                "CommonDI.h",
                "CopyLogsInteractorImpl.swift",
                "DobbyConfigsRepositoryImpl.swift",
                "IOSSessionShell.swift",
                "LoggerManagerImpl.swift",
                "SharedKeychainSecretStore.swift",
                "VpnManagerImpl.swift",
                "nativeModule.swift",
            ],
            sources: [
                "IOSLifecycleCore.swift",
                "IOSProviderSessionCoordinator.swift",
            ]
        ),
        .testTarget(
            name: "IOSLifecycleCoreTests",
            dependencies: ["IOSLifecycleCore"],
            path: "Tests/IOSLifecycleCoreTests"
        ),
    ]
)
