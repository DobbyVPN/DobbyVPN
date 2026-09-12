//go:build !dobbyvpn_test_seams

package runtime

// configureTestSeams is deliberately empty in ordinary product builds. The
// hardening fault injector is compiled only into an explicitly requested
// build-local qualification binary.
func configureTestSeams(*Options) {}
