@echo off

set DOBBYVPN_VERSION=%APP_MAJOR_VERSION%.%APP_MINOR_VERSION%.%APP_MAINTENANCE_VERSION%
echo [+] Building DobbyVPN v%DOBBYVPN_VERSION% MSI installers

:checkdeps
	echo [+] Checking wix
	wix --version || goto :error

	echo [+] Checking dobby-vpn
	if exist "dobbyVPN-windows.zip" (
		echo [+] Application zip file exist
		mkdir "dobbyVPN-windows"
		tar -xf "dobbyVPN-windows.zip" -C "dobbyVPN-windows" || goto :error
		if not exist "dobbyVPN-windows\bin\dobby-cli.exe" (
			echo [-] dobby-cli.exe not found in application zip
			cmd /c exit 1
			goto :error
		)
	) else (
		echo [+] Application zip file not exist
		goto :error
	)

	for %%F in (windows_grpcvpnserver.exe dobby_bridge.dll wintun.dll) do (
		if not exist "%%F" (
			echo [-] Required Windows service runtime %%F not found
			goto :error
		)
	)
	echo [+] Inserting windows_grpcvpnserver.exe and dobby_bridge.dll to the dobbyvpn application
	xcopy "windows_grpcvpnserver.exe" ".\dobbyVPN-windows\bin\" /Y || goto :error
	xcopy "dobby_bridge.dll" ".\dobbyVPN-windows\bin\" /Y || goto :error
	if not exist "wintun\bin\amd64\" mkdir "wintun\bin\amd64\" || goto :error
	echo [+] Inserting staged wintun.dll to the installer payload
	xcopy "wintun.dll" ".\wintun\bin\amd64\" /Y || goto :error

:build
	rem The desktop archive, VPN service, and bridge are built only for amd64.
	rem Do not publish an MSI whose native payload cannot run on its target CPU.
	call :msi amd64 x64 || goto :error

:success
	echo [+] Success.
	exit /b 0

:msi
	if not exist "bin\" mkdir "bin\"
	if not exist "bin\%~1" mkdir "bin\%~1"

	echo [+] Compiling %1
	wix build -src .\Package.wxs -src .\Folders.wxs -src .\AppComponents.wxs -b .\ -d "DOBBYVPN_PLATFORM=%1" -d "DOBBYVPN_VERSION=%DOBBYVPN_VERSION%" -arch %2 -o bin/%1/dobbyVPN-windows-%1.msi || goto :error
	goto :eof

:error
	echo [-] Failed.
	exit /b 1
