# gRPC server tester

## Prepare

```bash
go mod tidy
go mod download
```

## Run

It is required to run tester as superuser, cause it makes subprocess with vpn server

```bash
go build -o tester .
sudo ./tester -path="..."
```