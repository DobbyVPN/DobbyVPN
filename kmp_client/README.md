# Mobile client

A cross-platform VPN client built using Kotlin Multiplatform (KMP), sharing business logic across Android, iOS, and Desktop while using VPN protocols for each platform from [go_client/](../go_client/) library.

## Prerequisites

* Java 17+
* Golang
* Android SDK with NDK support

## Build

```bash
./gradlew assembleDebug
```

## Architecture

The project gradle layered architecture:

```
kmp_client/
├── app/ --- UI library
├── awg/ --- Library to import AmneziaWG tunnel golang code (legacy)
├── grpcprotos/ --- Library to generate automatic code for gRPC client
├── grpcstub/ --- Library-wrapper for gRPC client
├── iosApp/
├── outline/ --- Library to import Outline tunnel golang code
└── tap-device/
```
