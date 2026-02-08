#!/bin/bash

mkdir "bin/"
mkdir "bin/amd64"
mkdir "bin/aarch64"

echo [+] Extracting dobbyVPN-macos-aarch64.zip
unzip "dobbyVPN-macos-aarch64.zip" -d "bin/aarch64/"

echo [+] Switching workdir to bin/aarch64/
cd bin/aarch64/

echo [+] Making Scripts/ folder
mkdir Scripts
cp ../../postinstall.sh Scripts/

echo [+] Inserting vpnservice.plist file
cp ../../vpnservice.plist "Dobby Vpn.app/Contents/Resources/"

echo [+] Inserting macos_grpcvpnserver file
cp ../../macos_grpcvpnserver "Dobby Vpn.app/Contents/Resources/"

echo [+] Making Payload/ folder
mkdir Payload
cp -R "Dobby Vpn.app" Payload/

echo [+] Building aarch64 PGK installer
pkgbuild --root Payload \
         --scripts Scripts \
         --identifier com.dobby.pkg \
         --version $APP_MAJOR_VERSION.$APP_MINOR_VERSION.$APP_MAINTENANCE_VERSION \
         --install-location /Applications \
         dobbyVPN-macos-aarch64.pkg

cd ../../

echo [+] Extracting dobbyVPN-macos-amd64.zip
unzip "dobbyVPN-macos-amd64.zip" -d "bin/amd64/"

echo [+] Switching workdir to bin/amd64/
cd bin/amd64/

echo [+] Making Scripts/ folder
mkdir Scripts
cp ../../postinstall.sh Scripts/

echo [+] Inserting vpnservice.plist file
cp ../../vpnservice.plist "Dobby Vpn.app/Contents/Resources/"

echo [+] Inserting macos_grpcvpnserver file
cp ../../macos_grpcvpnserver "Dobby Vpn.app/Contents/Resources/"

echo [+] Making Payload/ folder
mkdir Payload
cp -R "Dobby Vpn.app" Payload/

echo [+] Building amd64 PGK installer
pkgbuild --root Payload \
         --scripts Scripts \
         --identifier com.dobby.pkg \
         --version $APP_MAJOR_VERSION.$APP_MINOR_VERSION.$APP_MAINTENANCE_VERSION \
         --install-location /Applications \
         dobbyVPN-macos-amd64.pkg

cd ../../
