// swift-tools-version: 5.9
import PackageDescription

// These tests intentionally model the boundary between the iOS shell and
// NetworkExtension without importing the app framework.  That makes the
// lifecycle contract runnable on every macOS CI runner, even when signing
// assets and generated KMP frameworks are not available.
let package = Package(
    name: "DobbyVPNLifecycleSeamTests",
    platforms: [.macOS(.v13)],
    products: [
        .library(name: "LifecycleSeams", targets: ["LifecycleSeams"]),
    ],
    targets: [
        .target(name: "LifecycleSeams"),
        .testTarget(name: "LifecycleSeamsTests", dependencies: ["LifecycleSeams"]),
    ]
)
