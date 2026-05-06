package proto

import (
	"context"
	"runtime/debug"

	"go_module/log"

	"google.golang.org/grpc"
)

// PanicRecoveryUnaryInterceptor returns a unary server interceptor that recovers from panics.
func PanicRecoveryUnaryInterceptor() grpc.UnaryServerInterceptor {
	return func(ctx context.Context, req interface{}, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (interface{}, error) {
		defer func() {
			if r := recover(); r != nil {
				log.Infof("PANIC recovered in %s: %v\nStack trace:\n%s",
					info.FullMethod, r, debug.Stack())
			}
		}()

		return handler(ctx, req)
	}
}

// ErrorLoggingUnaryInterceptor returns a unary server interceptor that logs errors.
func ErrorLoggingUnaryInterceptor() grpc.UnaryServerInterceptor {
	return func(ctx context.Context, req interface{}, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (interface{}, error) {
		log.Infof("gRPC call: %s", info.FullMethod)

		resp, err := handler(ctx, req)
		if err != nil {
			log.Infof("gRPC error in %s: %v", info.FullMethod, err)
		}

		return resp, err
	}
}
