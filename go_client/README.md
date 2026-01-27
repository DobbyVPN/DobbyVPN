# gRPC tunnels server

Server, that is been run with super user privileges, 
where all tunnels running and can be started/stopped via RPC calls

## API reference

```
// awg
rpc StartAwg (tunnel, config string)    returns ();
rpc StopAwg ()                          returns ();

// outline
rpc StartOutline (config string)        returns ();
rpc StopOutline ()                      returns ();

// health_check
rpc StartHealthCheck (period int32, sendMetrics bool)   returns ();
rpc StopHealthCheck ()                                  returns ();
rpc Status ()                                           returns (status string);
rpc TcpPing (address string)                            returns (result int32, error string);
rpc UrlTest (url string, standard int32 )               returns (result int32, error string);
rpc CouldStart ()                                       returns (result int32);

// cloak
rpc StartCloakClient (localHost string, localPort string , config string, udp bool) returns ();
rpc StopCloakClient ()                                                              returns ();    
```

## Build and run

#### Generate gRPC go files (if needed)

```bash
protoc --go_out=../ --go-grpc_out=../ vpnserver.proto
```

#### Build executable 

```bash
go build -o grpcvpnserver ./desktop_exports/...
```

## Installation

`grpcvpnserver` executable should be run in service/daemon, so it is been set up in the installers

### Linux intaller

#### Helpful documentation

##### How to create service config

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

##### How to enable and start service

```bash
sudo systemctl enable vpnserver.service
sudo systemctl start vpnserver.service
```

##### How to check if its working

```bash
systemctl status vpnserver.service
```

##### How to check stdout

```bash
sudo journalctl -xeu vpnserver.service
```

##### How to interrupt service

```bash
sudo systemctl stop vpnserver.service
```

##### How to remove service

```bash
sudo systemctl disable vpnserver.service
```

### MacOS intaller

#### Helpful documentation

##### How to create service config

Create service file `.../vpnserver.plist`
with following data

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
