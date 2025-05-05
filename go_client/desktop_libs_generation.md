```
Для запуска VPN нужно собрать библиотеки:

for Linux:
GOOS=linux GOARCH=amd64 go build -buildmode=c-shared -o lib_linux.so ./desktop_exports/...

for Windows:
$env:GOOS="windows"
$env:GOARCH="amd64"
go build -buildmode=c-shared -o lib_windows.dll ./desktop_exports/...

for MacOS:
GOOS=darwin GOARCH=amd64 go build -buildmode=c-shared -o lib_macos.dylib ./desktop_exports/...

потом получившиеся файлы .dll/.so (в зависимости от ОС) скопировать в ck-client-desktop-kotlin/Client/libs 

Возможные проблемы:
Если ругается на -buildmode=c-shared, то нужен компилятор для C, например, MinGW.
```