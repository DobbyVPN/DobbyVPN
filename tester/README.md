# gRPC server tester

This tester checks gRPC server via runnin different usage scenarios.

## Prepare

```bash
go mod tidy
go mod download
```

## Build

```bash
go build -o tester .
```

## Run

It is required to run tester as superuser, cause it makes subprocess with vpn server

```bash
sudo ./tester -path="..." -config="..."
```

### Program arguments

* `path` - absolute path to the grpc vpn server executable
* `config` - OPTIONAL argument, default vaule: `config.json`, provides config with all testing scenarios. Template for this config file can be found in the [config-template.json](./config-template.json) one.
