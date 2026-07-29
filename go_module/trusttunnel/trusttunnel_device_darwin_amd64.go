//go:build darwin && amd64 && !simulator

package trusttunnel

import (
	"bufio"
	"bytes"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/BurntSushi/toml"

	"go_module/auth"
	log "go_module/log"
	"go_module/trusttunnel/internal"
)

const (
	trustTunnelHelperEnv  = "DOBBYVPN_TRUSTTUNNEL_CLIENT"
	trustTunnelHelperName = "trusttunnel_client"
	trustTunnelReadyWait  = 12 * time.Second
	trustTunnelStopWait   = 5 * time.Second
)

// The seams are deliberately package-local.  They allow the Darwin-only tests
// to exercise the owned-process lifecycle without relying on a real endpoint.
var (
	trustTunnelCommand    = exec.Command
	trustTunnelExecutable = os.Executable
	trustTunnelGetenv     = os.Getenv
	trustTunnelDial       = net.DialTimeout
)

var trustTunnelProcessSequence atomic.Uint64

// TrustTunnelDevice exposes the same ProtocolDevice contract as the
// in-process implementation.  On Intel macOS the official client is an owned
// subprocess because the legacy go-go-tunnel archive is arm64-only.
type TrustTunnelDevice struct {
	mu sync.Mutex

	config     string
	proxyAddr  string
	svrIP      net.IP
	svrPort    int
	socksUser  string
	socksPass  string
	redactions []string
	helperPath string

	command    *exec.Cmd
	done       chan error
	stdout     io.ReadCloser
	stderr     io.ReadCloser
	tempDir    string
	configPath string
	label      string
}

func NewTrustTunnelDevice(trusttunnelConfig string) (*TrustTunnelDevice, error) {
	serverIPStr, err := internal.ExtractServerIP(trusttunnelConfig)
	if err != nil {
		return nil, errors.New("failed to extract TrustTunnel server address")
	}
	ip := net.ParseIP(serverIPStr)
	if ip == nil {
		return nil, errors.New("TrustTunnel server address is invalid")
	}

	helperPath, err := locateTrustTunnelHelper()
	if err != nil {
		return nil, err
	}

	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return nil, errors.New("failed to allocate local TrustTunnel SOCKS listener")
	}
	port := listener.Addr().(*net.TCPAddr).Port
	_ = listener.Close()

	user := auth.GenerateRandomAuth()
	password := auth.GenerateRandomAuth()
	rewritten, err := rewriteTrustTunnelSOCKSConfig(trusttunnelConfig, port, user, password)
	if err != nil {
		return nil, err
	}

	d := &TrustTunnelDevice{
		config:     rewritten,
		proxyAddr:  fmt.Sprintf("%s:%s@127.0.0.1:%d", user, password, port),
		svrIP:      ip,
		svrPort:    port,
		socksUser:  user,
		socksPass:  password,
		helperPath: helperPath,
		label:      fmt.Sprintf("tt-process-%d", trustTunnelProcessSequence.Add(1)),
	}
	// The proxy address is consumed by tun2socks and may appear in existing
	// lifecycle logs. Child output is untrusted too, so register sensitive
	// endpoint/configuration leaf values with the central redactor for this
	// process lifetime. No raw values are ever emitted here.
	d.redactions = trustTunnelRedactionWords(trusttunnelConfig, user, password)
	for _, word := range d.redactions {
		log.AddForbiddenWord(word)
	}
	log.Infof("trusttunnel", "[Intel macOS][TrustTunnel] helper validated; SOCKS bridge reserved process=%s", d.label)
	return d, nil
}

func locateTrustTunnelHelper() (string, error) {
	candidate := trustTunnelGetenv(trustTunnelHelperEnv)
	if candidate == "" {
		service, err := trustTunnelExecutable()
		if err != nil {
			return "", errors.New("unable to locate TrustTunnel helper beside VPN service")
		}
		candidate = filepath.Join(filepath.Dir(service), trustTunnelHelperName)
	}
	if !filepath.IsAbs(candidate) {
		return "", errors.New("TrustTunnel helper path must be absolute")
	}
	if err := validateTrustTunnelHelper(candidate); err != nil {
		return "", err
	}
	return candidate, nil
}

func validateTrustTunnelHelper(path string) error {
	info, err := os.Lstat(path)
	if err != nil {
		return errors.New("TrustTunnel helper is unavailable")
	}
	if !info.Mode().IsRegular() || info.Mode()&0o111 == 0 {
		return errors.New("TrustTunnel helper is not a regular executable")
	}
	if info.Mode()&0o022 != 0 {
		return errors.New("TrustTunnel helper permissions are unsafe")
	}
	// The launch daemon runs as root, while local development normally runs as
	// the desktop user. In either case the helper must be owned by this process.
	if stat, ok := info.Sys().(*syscall.Stat_t); !ok || int(stat.Uid) != os.Geteuid() {
		return errors.New("TrustTunnel helper owner does not match service owner")
	}
	return nil
}

func rewriteTrustTunnelSOCKSConfig(config string, port int, user, password string) (string, error) {
	var parsed map[string]interface{}
	if _, err := toml.Decode(config, &parsed); err != nil {
		return "", errors.New("failed to decode TrustTunnel configuration")
	}
	listenerRaw, ok := parsed["listener"]
	if !ok {
		listenerRaw = make(map[string]interface{})
		parsed["listener"] = listenerRaw
	}
	listener, ok := listenerRaw.(map[string]interface{})
	if !ok {
		return "", errors.New("TrustTunnel listener configuration is invalid")
	}
	socksRaw, ok := listener["socks"]
	if !ok {
		socksRaw = make(map[string]interface{})
		listener["socks"] = socksRaw
	}
	socks, ok := socksRaw.(map[string]interface{})
	if !ok {
		return "", errors.New("TrustTunnel SOCKS listener configuration is invalid")
	}
	socks["address"] = fmt.Sprintf("127.0.0.1:%d", port)
	socks["username"] = user
	socks["password"] = password

	var encoded bytes.Buffer
	if err := toml.NewEncoder(&encoded).Encode(parsed); err != nil {
		return "", errors.New("failed to encode TrustTunnel configuration")
	}
	return encoded.String(), nil
}

func trustTunnelRedactionWords(config string, generated ...string) []string {
	words := make(map[string]struct{})
	for _, word := range generated {
		if word != "" {
			words[word] = struct{}{}
		}
	}
	var parsed map[string]interface{}
	if _, err := toml.Decode(config, &parsed); err == nil {
		var collect func(key string, value interface{})
		collect = func(key string, value interface{}) {
			sensitive := false
			lowerKey := strings.ToLower(key)
			for _, marker := range []string{"endpoint", "address", "host", "server", "username", "password", "credential", "token", "secret", "certificate", "url"} {
				if strings.Contains(lowerKey, marker) {
					sensitive = true
					break
				}
			}
			switch typed := value.(type) {
			case map[string]interface{}:
				for nestedKey, nestedValue := range typed {
					collect(nestedKey, nestedValue)
				}
			case []interface{}:
				for _, nestedValue := range typed {
					collect(key, nestedValue)
				}
			case string:
				if sensitive && typed != "" {
					words[typed] = struct{}{}
				}
			}
		}
		for key, value := range parsed {
			collect(key, value)
		}
	}
	out := make([]string, 0, len(words))
	for word := range words {
		out = append(out, word)
	}
	return out
}

func (d *TrustTunnelDevice) Open(routingTableID int, uplinkIface string) error {
	if d == nil {
		return errors.New("trusttunnel device is not initialized")
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.command != nil {
		return errors.New("TrustTunnel process is already running")
	}
	// Close removes per-device forbidden words so they do not outlive the
	// session. Re-register them for a supported sequential reopen before any
	// child output or existing lifecycle logs can be emitted.
	for _, word := range d.redactions {
		log.AddForbiddenWord(word)
	}

	config, err := rewriteTrustTunnelRoutingConfig(d.config, routingTableID, uplinkIface)
	if err != nil {
		return err
	}
	if err := d.writeConfigLocked(config); err != nil {
		return err
	}

	cmd := trustTunnelCommand(d.helperPath, "--config", d.configPath)
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		d.removeTempArtifactsLocked()
		return errors.New("failed to capture TrustTunnel process output")
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		_ = stdout.Close()
		d.removeTempArtifactsLocked()
		return errors.New("failed to capture TrustTunnel process diagnostics")
	}
	if err := cmd.Start(); err != nil {
		_ = stdout.Close()
		_ = stderr.Close()
		d.removeTempArtifactsLocked()
		return errors.New("failed to start TrustTunnel helper")
	}

	d.command, d.stdout, d.stderr = cmd, stdout, stderr
	d.done = make(chan error, 1)
	d.streamProcessOutputLocked("stdout", stdout)
	d.streamProcessOutputLocked("stderr", stderr)
	go d.waitForProcess(cmd, d.done, d.label)

	if err := d.waitForSOCKSLocked(); err != nil {
		_ = d.closeLocked()
		return err
	}
	log.Infof("trusttunnel", "[Intel macOS][TrustTunnel] authenticated SOCKS listener ready process=%s", d.label)
	return nil
}

func rewriteTrustTunnelRoutingConfig(config string, tableID int, uplink string) (string, error) {
	var parsed map[string]interface{}
	if _, err := toml.Decode(config, &parsed); err != nil {
		return "", errors.New("failed to decode TrustTunnel configuration for routing")
	}
	routingRaw, ok := parsed["routing"]
	if !ok {
		routingRaw = make(map[string]interface{})
		parsed["routing"] = routingRaw
	}
	routing, ok := routingRaw.(map[string]interface{})
	if !ok {
		return "", errors.New("TrustTunnel routing configuration is invalid")
	}
	routing["routing_table_id"] = tableID
	if uplink != "" {
		routing["uplink_interface"] = uplink
	}
	var encoded bytes.Buffer
	if err := toml.NewEncoder(&encoded).Encode(parsed); err != nil {
		return "", errors.New("failed to encode TrustTunnel routing configuration")
	}
	return encoded.String(), nil
}

func (d *TrustTunnelDevice) writeConfigLocked(config string) error {
	dir, err := os.MkdirTemp("", "dobbyvpn-trusttunnel-")
	if err != nil {
		return errors.New("failed to create private TrustTunnel configuration directory")
	}
	if err := os.Chmod(dir, 0o700); err != nil {
		_ = os.RemoveAll(dir)
		return errors.New("failed to secure TrustTunnel configuration directory")
	}
	path := filepath.Join(dir, "client.toml")
	if err := os.WriteFile(path, []byte(config), 0o600); err != nil {
		_ = os.RemoveAll(dir)
		return errors.New("failed to write private TrustTunnel configuration")
	}
	if err := os.Chmod(path, 0o600); err != nil {
		_ = os.RemoveAll(dir)
		return errors.New("failed to secure TrustTunnel configuration")
	}
	d.tempDir, d.configPath = dir, path
	return nil
}

func (d *TrustTunnelDevice) streamProcessOutputLocked(stream string, reader io.ReadCloser) {
	label := d.label
	go func() {
		scanner := bufio.NewScanner(reader)
		scanner.Buffer(make([]byte, 1024), 64*1024)
		for scanner.Scan() {
			// All child output is routed through the central structured redactor.
			log.Debugf("trusttunnel", "[Intel macOS][TrustTunnel] process=%s stream=%s line=%s", label, stream, scanner.Text())
		}
		if err := scanner.Err(); err != nil {
			log.Debugf("trusttunnel", "[Intel macOS][TrustTunnel] process=%s stream=%s ended with read error type=%T", label, stream, err)
		}
	}()
}

func (d *TrustTunnelDevice) waitForProcess(cmd *exec.Cmd, done chan<- error, label string) {
	err := cmd.Wait()
	if err != nil {
		log.Warnf("trusttunnel", "[Intel macOS][TrustTunnel] process=%s exited unsuccessfully error_type=%T", label, err)
	} else {
		log.Debugf("trusttunnel", "[Intel macOS][TrustTunnel] process=%s exited", label)
	}
	done <- err
}

func (d *TrustTunnelDevice) waitForSOCKSLocked() error {
	deadline := time.NewTimer(trustTunnelReadyWait)
	defer deadline.Stop()
	tick := time.NewTicker(100 * time.Millisecond)
	defer tick.Stop()
	for {
		if err := d.authenticateSOCKS(); err == nil {
			return nil
		}
		select {
		case processErr := <-d.done:
			d.done = nil
			if processErr != nil {
				return errors.New("TrustTunnel helper exited before SOCKS listener became ready")
			}
			return errors.New("TrustTunnel helper stopped before SOCKS listener became ready")
		case <-deadline.C:
			return errors.New("timed out waiting for authenticated TrustTunnel SOCKS listener")
		case <-tick.C:
		}
	}
}

func (d *TrustTunnelDevice) authenticateSOCKS() error {
	conn, err := trustTunnelDial("tcp", fmt.Sprintf("127.0.0.1:%d", d.svrPort), 250*time.Millisecond)
	if err != nil {
		return err
	}
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(500 * time.Millisecond))
	if _, err := conn.Write([]byte{5, 1, 2}); err != nil {
		return err
	}
	reply := make([]byte, 2)
	if _, err := io.ReadFull(conn, reply); err != nil || reply[0] != 5 || reply[1] != 2 {
		return errors.New("SOCKS authentication method was not accepted")
	}
	if len(d.socksUser) > 255 || len(d.socksPass) > 255 {
		return errors.New("generated SOCKS authentication is invalid")
	}
	authRequest := make([]byte, 0, 3+len(d.socksUser)+len(d.socksPass))
	authRequest = append(authRequest, 1, byte(len(d.socksUser)))
	authRequest = append(authRequest, d.socksUser...)
	authRequest = append(authRequest, byte(len(d.socksPass)))
	authRequest = append(authRequest, d.socksPass...)
	if _, err := conn.Write(authRequest); err != nil {
		return err
	}
	if _, err := io.ReadFull(conn, reply); err != nil || reply[0] != 1 || reply[1] != 0 {
		return errors.New("SOCKS credentials were not accepted")
	}
	return nil
}

func (d *TrustTunnelDevice) GetServerIP() net.IP {
	if d == nil {
		return nil
	}
	return d.svrIP
}

func (d *TrustTunnelDevice) GetProxyAddr() string {
	if d == nil {
		return ""
	}
	return d.proxyAddr
}

func (d *TrustTunnelDevice) Close() error {
	if d == nil {
		return nil
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.closeLocked()
}

func (d *TrustTunnelDevice) closeLocked() error {
	cmd, done := d.command, d.done
	d.command, d.done = nil, nil
	if cmd != nil && cmd.Process != nil {
		_ = cmd.Process.Signal(os.Interrupt)
		if done != nil {
			select {
			case <-done:
			case <-time.After(trustTunnelStopWait):
				_ = cmd.Process.Kill()
				<-done
			}
		}
	}
	if d.stdout != nil {
		_ = d.stdout.Close()
		d.stdout = nil
	}
	if d.stderr != nil {
		_ = d.stderr.Close()
		d.stderr = nil
	}
	d.removeTempArtifactsLocked()
	for _, word := range d.redactions {
		log.RemoveForbiddenWord(word)
	}
	return nil
}

func (d *TrustTunnelDevice) removeTempArtifactsLocked() {
	if d.tempDir != "" {
		_ = os.RemoveAll(d.tempDir)
	}
	d.tempDir, d.configPath = "", ""
}
