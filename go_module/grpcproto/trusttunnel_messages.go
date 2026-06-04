package grpcproto

// GetTrustTunnelLastErrorResponse is the response for GetTrustTunnelLastError RPC.
type GetTrustTunnelLastErrorResponse struct {
	Error string `protobuf:"bytes,1,opt,name=error,proto3" json:"error,omitempty"`
}

func (x *GetTrustTunnelLastErrorResponse) Reset()         {}
func (x *GetTrustTunnelLastErrorResponse) String() string { return x.Error }
func (x *GetTrustTunnelLastErrorResponse) ProtoMessage()  {}
func (x *GetTrustTunnelLastErrorResponse) GetError() string {
	if x != nil {
		return x.Error
	}
	return ""
}

// StartTrustTunnelRequest is the request for StartTrustTunnel RPC.
type StartTrustTunnelRequest struct {
	Config string `protobuf:"bytes,1,opt,name=config,proto3" json:"config,omitempty"`
}

func (x *StartTrustTunnelRequest) Reset()         {}
func (x *StartTrustTunnelRequest) String() string { return x.Config }
func (x *StartTrustTunnelRequest) ProtoMessage()  {}
func (x *StartTrustTunnelRequest) GetConfig() string {
	if x != nil {
		return x.Config
	}
	return ""
}

// StartTrustTunnelResponse is the response for StartTrustTunnel RPC.
type StartTrustTunnelResponse struct {
	Result int32 `protobuf:"varint,1,opt,name=result,proto3" json:"result,omitempty"`
}

func (x *StartTrustTunnelResponse) Reset()         {}
func (x *StartTrustTunnelResponse) String() string { return "" }
func (x *StartTrustTunnelResponse) ProtoMessage()  {}
func (x *StartTrustTunnelResponse) GetResult() int32 {
	if x != nil {
		return x.Result
	}
	return 0
}
