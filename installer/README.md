# Windows MSI installer

## Prerequisites

* Wix v5
* Wintun
* Prebuilt application

### Install wix

#### Via command-line .NET tool

```bash
dotnet tool install --global wix
wix --version
```

### Install wintun

```bash
wget -O wintun.zip https://www.wintun.net/builds/wintun-0.14.1.zip
tar -xvzf wintun.zip
```

### Download prebuild application

1. Build grpc vpn server. Follow [this steps](../go_client/README.md)
2. Build Kotlin Multiplatform client. Follow [this steps](../kmp_client/README.md)
3. Extract build `dobby-vpn-1.1-windows-amd64.zip` file to the `dobby-vpn/` folder

## Build MSI

### Properties, that should be pre defined

* Architecture platform (x86, arm64, arm, amd64)
* Application version

### Build command

```
mkdir -p bin\<Platform>
wix build -src .\\Package.wxs -src .\\Folders.wxs -src .\\AppComponents.wxs -b .\\ -d "DOBBYVPN_PLATFORM=<Platform>" -d "DOBBYVPN_VERSION=<Version>" -arch <Version>  -o bin/<Version>/DobbyVPNInstaller.msi
```
