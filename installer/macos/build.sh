#!/bin/bash

mkdir "bin/"
mkdir "bin/amd64"
mkdir "bin/aarch64"

echo [+] Extracting dobby-vpn-1.1-mac-aarch64.zip
unzip "dobby-vpn-1.1-mac-aarch64.zip" -d "bin/aarch64/"

cd  bin/aarch64/

echo [+] Making Payload/ folder
mkdir Payload
cp -R "Dobby Vpn.app" Payload/

echo [+] Making Scripts/ folder
mkdir Scripts
cp -R ../../postinstall.sh Scripts/

echo [+] Building aarch64 PGK installer
pkgbuild --root Payload \
         --scripts Scripts \
         --identifier com.dobby.pkg \
         --version $APP_MAJOR_VERSION.$APP_MINOR_VERSION.$APP_MAINTENANCE_VERSION \
         --install-location /Applications \
         dobbyVPN-macos-aarch64.pkg

cd ../../

echo [+] Extracting dobby-vpn-1.1-mac-amd64.zip
unzip "dobby-vpn-1.1-mac-amd64.zip" -d "bin/amd64/"

cd  bin/amd64/

echo [+] Making Payload/ folder
mkdir Payload
cp -R "Dobby Vpn.app" Payload/

echo [+] Making Scripts/ folder
mkdir Scripts
cp -R ../../postinstall.sh Scripts/

echo [+] Building amd64 PGK installer
pkgbuild --root Payload \
         --scripts Scripts \
         --identifier com.dobby.pkg \
         --version $APP_MAJOR_VERSION.$APP_MINOR_VERSION.$APP_MAINTENANCE_VERSION \
         --install-location /Applications \
         dobbyVPN-macos-amd64.pkg

cd ../../
