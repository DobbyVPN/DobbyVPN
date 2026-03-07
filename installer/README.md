# Installer Build Scripts

This directory contains all scripts and configuration files required to build application installers for supported platforms:

* Windows
* MacOS

These scripts automate packaging and generating distributable installer files.

## Folder Structure

```
installers/
├── windows/
│   ├── .gitignore 
│   ├── AppComponents.wxs 
│   ├── build.bat 
│   ├── Folders.wxs 
│   ├── Package.wxs 
│   └── README.md
│
├── macos/
│   ├── .gitignore 
│   ├── build.sh 
│   ├── postinstall.sh 
│   ├── README.md 
│   └── vpnservice.plist
│
└── README.md
```

## Supported Platforms

| Platform | Output Format | Architecture | Status |
| --- | --- | --- | --- |
| Windows   | .msi | x86        | ✅ Supported | 
| Windows   | .msi | amd64      | ✅ Supported | 
| Windows   | .msi | arm64      | ✅ Supported | 
| MacOS     | .pkg | amd64      | ✅ Supported | 
| MacOS     | .pkg | aarch64    | ✅ Supported | 

## Windows Installer

### Requirements

* Installer tool (WiX)

### Build 

```powershell
cd windows/
./build.bat
```

### Environment variables

* APP_MAJOR_VERSION
* APP_MINOR_VERSION
* APP_MAINTENANCE_VERSION
* GITHUB_SHA
* GITHUB_REPOSITORY

### Output
```
installers/
├── windows/
│   ├── bin/
│   │   ├── x86/
│   │   │   └── dobbyVPN-windows-x86.msi
│   │   ├── amd64/
│   │   │   └── dobbyVPN-windows-amd64.msi
│   │   ├── arm64/
└── └── └── └── dobbyVPN-windows-arm64.msi
```

## MacOS Installer

### Environment variables

* APP_MAJOR_VERSION
* APP_MINOR_VERSION
* APP_MAINTENANCE_VERSION

### Build 

```bash
cd macos/
sh build.sh
```

### Output

```
installers/
├── macos/
│   ├── bin/
│   │   ├── amd64/
│   │   │   └── dobbyVPN-macos-amd64.pkg
│   │   ├── aarch64/
└── └── └── └── dobbyVPN-macos-aarch64.pkg
```

## Notes

* All installers not only installs application, it runs gRPC vpn service.
* Windows uninstaller also removed installed service.  
* There is no MacOS uninstaller, so user should remove vpn service by himself.  
