package protected_dialer

import (
	"context"
	"errors"
	"sync"
)

// DestinationPermissions is the platform filter owned by one VPN network.
// An attempt may authorize actual transport destinations and retires those
// permissions only after its protocol and application flows have closed.
type DestinationPermissions interface {
	Authorize(context.Context, uint64, string, string) error
	Retire(context.Context, uint64) error
}

var destinationOwner struct {
	sync.Mutex
	permissions DestinationPermissions
}

func SetDestinationPermissions(owner DestinationPermissions) {
	destinationOwner.Lock()
	destinationOwner.permissions = owner
	destinationOwner.Unlock()
}

type scopeKey struct{}

type SocketScope struct {
	mu          sync.Mutex
	ctx         context.Context
	cancel      context.CancelFunc
	generation  uint64
	protect     func(context.Context, int) error
	permissions DestinationPermissions
	closed      bool
}

func NewSocketScope(parent context.Context, generation uint64, protect func(context.Context, int) error) (context.Context, *SocketScope) {
	ctx, cancel := context.WithCancel(parent)
	destinationOwner.Lock()
	permissions := destinationOwner.permissions
	destinationOwner.Unlock()
	scope := &SocketScope{ctx: ctx, cancel: cancel, generation: generation, protect: protect, permissions: permissions}
	return context.WithValue(ctx, scopeKey{}, scope), scope
}

func (s *SocketScope) Close(ctx context.Context) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.cancel()
	if s.closed {
		return nil
	}
	if s.permissions != nil {
		if err := s.permissions.Retire(ctx, s.generation); err != nil {
			return err
		}
	}
	s.closed = true
	return nil
}

func withSocketScope(ctx, owner context.Context) context.Context {
	if owner == nil {
		return ctx
	}
	if scope, ok := owner.Value(scopeKey{}).(*SocketScope); ok {
		return context.WithValue(ctx, scopeKey{}, scope)
	}
	return ctx
}

func (s *SocketScope) protectFD(ctx context.Context, fd uintptr, network, address string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return errors.New("socket attempt is retired")
	}
	if err := errors.Join(ctx.Err(), s.ctx.Err()); err != nil {
		return err
	}
	if isLoopback(address) {
		return nil
	}
	if s.permissions != nil {
		if err := s.permissions.Authorize(ctx, s.generation, network, address); err != nil {
			return err
		}
	}
	if s.protect != nil {
		if fd > uintptr(^uint(0)>>1) {
			return errors.New("socket descriptor overflows int")
		}
		return s.protect(ctx, int(fd))
	}
	return protectPlatformFD(fd, network, address)
}

func ProtectRawConnContext(ctx context.Context, network, address string, connection interface{ Control(func(uintptr)) error }) error {
	var protectionErr error
	if err := connection.Control(func(fd uintptr) { protectionErr = protectContextFD(ctx, fd, network, address) }); err != nil {
		return err
	}
	return protectionErr
}

func protectContextFD(ctx context.Context, fd uintptr, network, address string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if scope, ok := ctx.Value(scopeKey{}).(*SocketScope); ok {
		return scope.protectFD(ctx, fd, network, address)
	}
	return protectPlatformFD(fd, network, address)
}

// Native libraries that expose only the descriptor supply their retained,
// numeric upstream inventory. No global "current attempt" callback is used.
func ProtectSocketDestinations(ctx context.Context, fd int, addresses []string) error {
	network, err := socketNetworkFromDescriptor(fd)
	if err != nil {
		return err
	}
	if len(addresses) == 0 {
		return errors.New("native socket has no authorized destinations")
	}
	for _, address := range addresses {
		if err := protectContextFD(ctx, uintptr(fd), network, address); err != nil {
			return err
		}
	}
	return nil
}
