//go:build !windows

package main

import (
	"go_module/desktop_exports/client"

	"google.golang.org/grpc"
)

func dialService() (*grpc.ClientConn, error) {
	return client.Dial()
}
