# Guide to Running the iOS App Locally

## Installing Dependencies

### MyLibrary.xcframework

This is the compiled Go part of our project. It is taken from CI, where it is stored as an artifact.

### app.framework

This is the compiled Kotlin part of our project. It is built locally. You need to install the SDK and, while being in the `kmp_client` directory, run
`./gradlew linkReleaseFrameworkIosArm64`,
then copy the resulting framework into `iosApp`.

## Certificate Setup

You need to generate a **Development** certificate on the
[Apple Developer](https://developer.apple.com/account/resources/profiles/list) website.

In Xcode, open the app settings and go to the `Signing & Capabilities` section.
In the `Signing (Debug)` block, under `iOS`, you need to sign in with your Apple developer account.
