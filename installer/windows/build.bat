@echo off

set DOBBYVPN_VERSION=%APP_MAJOR_VERSION%.%APP_MINOR_VERSION%.%APP_MAINTENANCE_VERSION%
echo [+] Building DobbyVPN v%DOBBYVPN_VERSION% MSI installers

:checkdeps
	echo [+] Checking wix
	wix --version || goto :error
	
	if exist "wintun\" echo [+] Wintun installed
	if not exist "wintun\" call :wintun

	echo [+] Checking dobby-vpn
	if exist "dobbyVPN-windows.zip" (
		echo [+] Application zip file exist
		mkdir "dobbyVPN-windows"
		tar -xf "dobbyVPN-windows.zip" -C "dobbyVPN-windows" || goto :error
	) else (
		echo [+] Application zip file not exist
		goto :error
	)

	echo [+] Checking grpcvpnserver.exe
	if exist "grpcvpnserver.exe" (
		echo [+] Inserting grpcvpnserver.exe to the dobbyvpn application
		xcopy "grpcvpnserver.exe" ".\dobbyVPN-windows\bin\." /Y
	) else (
		echo [+] grpcvpnserver.exe not exist
		goto :error
	)

:build
	call :msi amd64 x64 || goto :error
	call :msi x86 x86 || goto :error
	call :msi arm64 arm64 || goto :error

:success
	echo [+] Success.
	exit /b 0

:msi
	if not exist "bin\" mkdir "bin\"
	if not exist "bin\%~1" mkdir "bin\%~1"

	echo [+] Compiling %1
	wix build -src .\Package.wxs -src .\Folders.wxs -src .\AppComponents.wxs -b .\ -d "DOBBYVPN_PLATFORM=%1" -d "DOBBYVPN_VERSION=%DOBBYVPN_VERSION%" -arch %2 -o bin/%1/dobbyVPN-windows-%1.msi || goto :error
	goto :eof

:wintun
	echo [+] Downloading wintun
	curl -#fLo wintun.zip https://www.wintun.net/builds/wintun-0.14.1.zip || goto :error
	echo [+] Unzip wintun
	tar -xvzf wintun.zip || goto :error
	echo [+] Clear wintun.zip
	del wintun.zip
	goto :eof

:error
	echo [-] Failed with error #%errorlevel%.
	cmd /c exit %errorlevel%
