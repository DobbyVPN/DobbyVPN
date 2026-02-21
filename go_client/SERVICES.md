# How to run executable in service on different platforms

## Linux

### How to create service config

Create service file `/etc/systemd/system/vpnserver.service`
with following data

```
[Unit]
Description=DobbyVPN dervice

[Service]
ExecStart=<Service executable path>
Restart=always

[Install]
Alias=vpnserver.service
WantedBy=multi-user.target
```

It can be created with symbolic link

```bash
sudo ln -s <vpnserver.service path> /etc/systemd/system/vpnserver.service
```

### How to enable and start service

```bash
sudo systemctl enable vpnserver.service
sudo systemctl start vpnserver.service
```

### How to check if its working

```bash
systemctl status vpnserver.service
```

### How to check stdout

```bash
sudo journalctl -xeu vpnserver.service
```

### How to interrupt service

```bash
sudo systemctl stop vpnserver.service
```

### How to remove service

```bash
sudo systemctl disable vpnserver.service
```

## MacOS intaller

### How to create service config

Create service file `.../vpnserver.plist` with following data

```xml
<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
    <key>Label</key>
    <string>dobbyvpn</string>
    <key>ProgramArguments</key>
    <array>
        <string>...</string>
    </array>

    <key>RunAtLoad</key>
    <true/>
    <key>KeepAlive</key>
    <true/>

    <key>WorkingDirectory</key>
    <string>ServiceWorkingDir</string>

    <key>StandardOutPath</key>
    <string>ServiceStdoutPath</string>
    <key>StandardErrorPath</key>
    <string>ServiceStderrPath</string>
</dict>
</plist>
```

And run this command:

```bash
launchctl load "$HOME/Library/LaunchAgents/vpnservice.plist"
```

## Windows

### Create vpn service

```bash
sc.exe create "DobbyVPN Server" binPath="...\windows_grpcvpnserver.exe -mode=service" type=own start=auto error=normal depend=nsi/tcpip displayname="DobbyVPN gRPC Server"
sc.exe sidtype "DobbyVPN Server" unrestricted
sc.exe start "DobbyVPN Server"
```

### Stop vpn service

```bash
sc.exe stop "DobbyVPN Server"
sc.exe delete "DobbyVPN Server"
```
