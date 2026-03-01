FROM golang:1.24-alpine

WORKDIR /src
COPY Cloak /src/Cloak

WORKDIR /src/Cloak
RUN go build -o /usr/local/bin/ck-server ./cmd/ck-server
COPY go_client/e2e/cloak/ckserver.e2e.json /etc/ckserver.e2e.json

ENTRYPOINT ["/usr/local/bin/ck-server", "-c", "/etc/ckserver.e2e.json"]
