FROM golang:1.24-alpine AS build
WORKDIR /app
COPY main.go .
RUN go mod init wss_echo && \
    go get github.com/gorilla/websocket@v1.5.3 && \
    go build -o /wss_echo main.go

FROM alpine:3.20
COPY --from=build /wss_echo /wss_echo
EXPOSE 8443
ENTRYPOINT ["/wss_echo"]
