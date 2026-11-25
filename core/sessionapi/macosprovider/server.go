package macosprovider

import (
	"context"
	"crypto/rand"
	"encoding/gob"
	"encoding/hex"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"sync"

	"core/iprange"
	"core/log"
	"core/sessionapi"
)

// Server lives inside a single provider incarnation. The tunnel holds the
// installer-file process lock before opening this root-private socket.
type Server struct {
	mu            sync.Mutex
	session       string
	instance      string
	runtime       sessionapi.Runtime
	network       sessionapi.RuntimeLease
	generation    uint64
	attempt       sessionapi.RuntimeLease
	attemptCancel context.CancelFunc
	http          *http.Server
	cancel        context.CancelFunc
	listener      net.Listener
}

func NewServer(ctx context.Context, key string, ref sessionapi.SessionRef, rt sessionapi.Runtime) (*Server, error) {
	path, err := socketPath(key)
	if err != nil {
		return nil, err
	}
	if ref.SessionID == "" || ref.Generation == 0 {
		return nil, errors.New("missing desktop network generation")
	}
	if i, err := os.Lstat(path); err == nil {
		if i.Mode()&os.ModeSocket == 0 {
			return nil, fmt.Errorf("unexpected object at packet provider socket")
		}
		if err := os.Remove(path); err != nil {
			return nil, err
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}
	l, err := net.Listen("unix", path)
	if err != nil {
		return nil, err
	}
	network, err := rt.(sessionapi.NetworkRuntime).PrepareNetwork(ctx, ref, sessionapi.RuntimeProfile{})
	if err != nil {
		if network != nil {
			err = errors.Join(err, network.Stop(sessionapi.CleanupContext(ctx)))
		}
		return nil, errors.Join(err, l.Close())
	}
	var nonce [16]byte
	rand.Read(nonce[:])
	lifetime, cancel := context.WithCancel(context.Background())
	s := &Server{session: ref.SessionID, instance: hex.EncodeToString(nonce[:]), runtime: rt,
		network: network, cancel: cancel, listener: l}
	s.http = &http.Server{Handler: http.HandlerFunc(s.handle), BaseContext: func(net.Listener) context.Context { return lifetime }}
	go func() {
		if err := s.http.Serve(l); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Errorf("MacPacketProvider", "Local runtime server: %v", err)
		}
	}()
	return s, nil
}

func (s *Server) Close(ctx context.Context) error {
	s.cancel()
	if err := s.http.Close(); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.stopAttempt(ctx); err != nil {
		return err
	}
	return s.network.Stop(ctx)
}

func (s *Server) stopAttempt(ctx context.Context) error {
	if s.attempt != nil {
		if err := s.attempt.Stop(ctx); err != nil {
			return err
		}
		s.attempt = nil
	}
	if s.attemptCancel != nil {
		s.attemptCancel()
		s.attemptCancel = nil
	}
	return nil
}

func (s *Server) handle(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost || r.URL.Path != "/runtime" {
		http.NotFound(w, r)
		return
	}
	var op operation
	if err := gob.NewDecoder(r.Body).Decode(&op); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if op.Ref.SessionID != s.session {
		http.Error(w, "stale desktop session", http.StatusConflict)
		return
	}
	if op.Action == "identity" {
		_, _ = w.Write([]byte(s.instance))
		return
	}
	if op.Instance != s.instance {
		// A dead provider cannot retain resources from its former process.
		if op.Action == "stop" {
			return
		}
		http.Error(w, "packet provider restarted", http.StatusConflict)
		return
	}
	var health <-chan error
	s.mu.Lock()
	err := s.executeLocked(r.Context(), op, &health)
	s.mu.Unlock()
	if health != nil && err == nil {
		select {
		case failure, ok := <-health:
			if !ok {
				err = errors.New("packet provider stopped monitoring the connection")
			} else {
				err = failure
			}
		case <-r.Context().Done():
			err = r.Context().Err()
		}
	}
	if err != nil {
		w.Header().Set("Dobby-Failure", string(sessionapi.CodeOf(err)))
		http.Error(w, err.Error(), http.StatusConflict)
	}
}

func (s *Server) executeLocked(ctx context.Context, op operation, health *<-chan error) error {
	gen := op.Ref.Generation
	if gen == 0 {
		return errors.New("missing protocol generation")
	}
	switch op.Action {
	case "start":
		if gen <= s.generation || s.attempt != nil {
			return errors.New("overlapping or retired protocol generation")
		}
		s.generation = gen
		profile := sessionapi.RuntimeProfile{Summary: op.Summary, Config: op.Config,
			ExcludeSet: iprange.New(op.Excluded), ExcludeHosts: op.Hosts}
		var err error
		lifetime, cancel := context.WithCancel(context.WithoutCancel(ctx))
		s.attemptCancel = cancel
		abandon := context.AfterFunc(ctx, cancel)
		s.attempt, err = s.runtime.Start(lifetime, op.Ref, profile)
		abandon()
		if s.attempt == nil {
			cancel()
			s.attemptCancel = nil
		}
		return err
	case "stop":
		if gen < s.generation {
			return nil
		}
		if gen > s.generation && s.attempt != nil {
			return errors.New("another protocol generation owns the network")
		}
		// A cancellation can arrive before the corresponding start request.
		s.generation = gen
		return s.stopAttempt(ctx)
	}
	if gen != s.generation || s.attempt == nil {
		return errors.New("protocol generation is no longer active")
	}
	switch op.Action {
	case "health":
		lease, ok := s.attempt.(sessionapi.HealthMonitoringLease)
		if !ok {
			return errors.New("protocol health monitor unavailable")
		}
		*health = lease.HealthFailures()
		return nil
	case "probe":
		if lease, ok := s.attempt.(sessionapi.ProbingLease); ok {
			lease.ProbeNow(op.Reason)
		}
		return nil
	default:
		return fmt.Errorf("unknown packet provider operation %q", op.Action)
	}
}
