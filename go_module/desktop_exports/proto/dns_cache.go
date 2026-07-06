//go:build !(android || ios)

package proto

import (
	"context"

	"go_module/desktop_exports/api"
	"go_module/desktop_exports/common"
	"go_module/grpcproto"
	"go_module/log"
)

func (s *Server) ClearDNSCache(_ context.Context, in *grpcproto.Empty) (*grpcproto.Empty, error) {
	log.Debugf(common.Category, "ClearDNSCache")
	api.ClearDNSCache()

	return &grpcproto.Empty{}, nil
}

func (s *Server) SetDNSCacheEntries(_ context.Context, in *grpcproto.SetDNSCacheEntriesRequest) (*grpcproto.SetDNSCacheEntriesResponse, error) {
	count := api.SetDNSCacheEntries(in.GetEntries(), in.GetSource())
	log.Debugf(common.Category, "SetDNSCacheEntries cached=%d source=%s", count, in.GetSource())

	return &grpcproto.SetDNSCacheEntriesResponse{
		CachedCount: count,
	}, nil
}
