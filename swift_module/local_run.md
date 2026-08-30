# Guide to Running the iOS App Locally

## v1.5.0 iOS coverage boundary

No physical iPhone is available for v1.5.0. Do not add or infer a physical
NetworkExtension packet-tunnel pass from Simulator, framework, IPA, or
screenshot evidence. The available signed-IPA and Simulator/build checks must
still pass and the physical traffic gap must remain visible in release status.

## Installing Dependencies

### DobbyVPNRuntime.xcframework

This is the compiled Go part of our project. It is taken from CI, where it is
stored as an artifact. To build it locally from `go_module/` on a Mac with
Xcode and gomobile installed, run:

```bash
./scripts/build_ios_xcframework.sh
```

Copy the resulting `DobbyVPNRuntime.xcframework` into `swift_module/`.

### app.framework

This is the compiled Kotlin part of our project. Build the target matching the
SDK you intend to use, then copy the output to `swift_module/app.framework`:

```bash
# Physical iOS
./gradlew :app:linkReleaseFrameworkIosArm64
ditto app/build/bin/iosArm64/releaseFramework/app.framework ../swift_module/app.framework

# Apple-silicon iOS Simulator
./gradlew :app:linkDebugFrameworkIosSimulatorArm64
ditto app/build/bin/iosSimulatorArm64/debugFramework/app.framework ../swift_module/app.framework

# Intel iOS Simulator
./gradlew :app:linkDebugFrameworkIosX64
ditto app/build/bin/iosX64/debugFramework/app.framework ../swift_module/app.framework
```

The Simulator can build and install the app and run shared-code tests, but it
cannot run a Packet Tunnel or a real TrustTunnel connection.

## Certificate Setup

You need to generate a **Development** certificate on the [Apple Developer](https://developer.apple.com/account/resources/profiles/list) website.

In Xcode, open the app settings and go to the `Signing & Capabilities` section.
In the `Signing (Debug)` block, under `iOS`, you need to sign in with your Apple developer account.
