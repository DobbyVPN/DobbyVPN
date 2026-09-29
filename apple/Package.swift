// swift-tools-version: 5.9
import PackageDescription

// Compile the platform-neutral protocol and lifecycle-diagnostic sources that
// are also used by the production CommonDI target. This catches drift without
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
        .target(
            name: "DobbyNativeUI",
            path: "NativeUI"
        ),
        .executableTarget(
            name: "DobbyVPNMacApp",
            dependencies: ["DobbyNativeUI"],
            path: "macOSApp"
        ),
        .target(
            name: "IOSLifecycleCore",
            path: "CommonDI",
            exclude: [
                "CommonDI.h",
                "DobbyConfigsRepositoryImpl.swift",
                "IOSSessionShell.swift",
                "SharedKeychainSecretStore.swift",
                "VpnManagerImpl.swift",
                "AppCompositionRoot.swift",
            ],
            sources: [
                "IOSProviderMessageProtocol.swift",
                "IOSLifecycleDiagnostics.swift",
            ]
        ),
        .testTarget(
            name: "IOSLifecycleCoreTests",
            dependencies: ["IOSLifecycleCore"],
            path: "Tests/IOSLifecycleCoreTests"
        ),
    ]
)
