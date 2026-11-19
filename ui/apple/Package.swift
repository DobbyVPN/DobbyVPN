// swift-tools-version: 5.9
import PackageDescription

let package = Package(
    name: "DobbyVPNIOSLifecycleCore",
    platforms: [.macOS(.v12)],
    products: [
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
        .executableTarget(
            name: "DobbyVPNMacApp",
            dependencies: ["DobbyNativeUI", "DobbyDiagnosticFiles"],
            path: "macos/app"
        ),
    ]
)
