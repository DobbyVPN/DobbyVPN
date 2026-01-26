@echo off

set DOBBYVPN_VERSION="0.0.27"

:checkdeps
	echo [+] Checking wix
	wix --version || goto :error
	echo [+] Downloading wintun
	curl -#fLo wintun.zip https://www.wintun.net/builds/wintun-0.14.1.zip || goto :error
	tar -xvzf wintun.zip || goto :error
	echo [+] Checking dobby-vpn
	if not exist dobby-vpn\ goto :error

:build
	call :msi amd64 x64 || goto :error
	@REM call :msi x86 x86 || goto :error
	@REM call :msi arm64 arm64 || goto :error

:success
	echo [+] Success.
	exit /b 0

:msi
	if not exist "bin\" mkdir "bin\"
	if not exist "bin\%~1" mkdir "bin\%~1"

	echo [+] Compiling %1
	wix build -src .\Package.wxs -src .\Folders.wxs -src .\AppComponents.wxs -b .\ -d "DOBBYVPN_PLATFORM=%1" -d "DOBBYVPN_VERSION=%DOBBYVPN_VERSION%" -arch %2 -o bin/%1/dobby-vpn-%DOBBYVPN_VERSION%.msi || goto :error
	goto :eof

:error
	echo [-] Failed with error #%errorlevel%.
	cmd /c exit %errorlevel%
