package docker

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"os"
	"os/user"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/flbraun/dviz/internal/config"
	"golang.org/x/crypto/ssh"
	"golang.org/x/crypto/ssh/agent"
	"golang.org/x/crypto/ssh/knownhosts"
)

const (
	sshConnectTimeout = 15 * time.Second
	sshKeepalive      = 30 * time.Second
)

// sshDialer reaches a remote Docker socket through an SSH connection, using OpenSSH's
// stream-local forwarding (the mechanism behind `ssh -L local.sock:/var/run/docker.sock`).
// The remote host needs sshd with stream-local forwarding enabled (the default), not the
// docker CLI.
type sshDialer struct {
	addr   string
	socket string
	cfg    *ssh.ClientConfig
	agent  net.Conn // open ssh-agent connection; agent signers need it while signing

	mu     sync.Mutex
	client *ssh.Client
	closed bool
}

func newSSHDialer(h config.Host) (*sshDialer, error) {
	username, addr, socket, err := h.SSHTarget()
	if err != nil {
		return nil, err
	}
	if username == "" {
		username = currentUser()
	}
	opts := h.SSH
	d := &sshDialer{addr: addr, socket: socket}

	auth, err := d.authMethod(opts)
	if err != nil {
		d.closeAgent()
		return nil, err
	}
	d.cfg = &ssh.ClientConfig{
		User:    username,
		Auth:    auth,
		Timeout: sshConnectTimeout,
	}
	if opts.InsecureIgnoreHostKey {
		d.cfg.HostKeyCallback = ssh.InsecureIgnoreHostKey() //nolint:gosec // opt-in via config
	} else if err := d.verifyHostKeys(opts.KnownHosts); err != nil {
		d.closeAgent()
		return nil, err
	}
	return d, nil
}

func currentUser() string {
	if u, err := user.Current(); err == nil && u.Username != "" {
		return u.Username
	}
	return os.Getenv("USER")
}

// authMethod builds the authentication configured by ssh.method. Nothing else is tried.
func (d *sshDialer) authMethod(opts *config.SSH) ([]ssh.AuthMethod, error) {
	switch opts.Method {
	case config.SSHMethodPassword:
		pw, err := readSecretFile(opts.Password)
		if err != nil {
			return nil, err
		}
		return []ssh.AuthMethod{ssh.Password(pw)}, nil

	case config.SSHMethodAgent:
		sock := os.Getenv("SSH_AUTH_SOCK")
		if sock == "" {
			return nil, errors.New("ssh: method agent needs a running ssh-agent (SSH_AUTH_SOCK is not set)")
		}
		conn, err := net.Dial("unix", sock)
		if err != nil {
			return nil, fmt.Errorf("ssh: cannot reach ssh-agent at %s: %w", sock, err)
		}
		d.agent = conn
		return []ssh.AuthMethod{ssh.PublicKeysCallback(agent.NewClient(conn).Signers)}, nil

	case config.SSHMethodKey:
		passphrase := ""
		if opts.Password != "" {
			var err error
			if passphrase, err = readSecretFile(opts.Password); err != nil {
				return nil, err
			}
		}
		s, err := loadKey(opts.IdentityFile, passphrase)
		if err != nil {
			return nil, err
		}
		return []ssh.AuthMethod{ssh.PublicKeys(s)}, nil
	}
	return nil, fmt.Errorf("ssh: unsupported method %q", opts.Method)
}

// readSecretFile reads a password or passphrase file, dropping one trailing newline.
func readSecretFile(path string) (string, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf("ssh password file: %w", err)
	}
	s := strings.TrimSuffix(strings.TrimSuffix(string(b), "\n"), "\r")
	if s == "" {
		return "", fmt.Errorf("ssh password file %s is empty", path)
	}
	return s, nil
}

func loadKey(path, passphrase string) (ssh.Signer, error) {
	pem, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("ssh identity: %w", err)
	}
	var s ssh.Signer
	if passphrase != "" {
		s, err = ssh.ParsePrivateKeyWithPassphrase(pem, []byte(passphrase))
	} else {
		s, err = ssh.ParsePrivateKey(pem)
	}
	var missing *ssh.PassphraseMissingError
	if errors.As(err, &missing) {
		return nil, fmt.Errorf("ssh identity %s is passphrase-protected; set ssh.password to a file containing the passphrase", path)
	}
	if err != nil {
		return nil, fmt.Errorf("ssh identity %s: %w", path, err)
	}
	return s, nil
}

// verifyHostKeys configures host key checking against a known_hosts file and restricts the
// negotiated host key algorithms to the ones known for this host, so that a host listed
// with e.g. only an ed25519 key is not rejected for presenting its RSA key.
func (d *sshDialer) verifyHostKeys(file string) error {
	if file == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return fmt.Errorf("ssh: cannot locate known_hosts: %w; set ssh.known_hosts", err)
		}
		file = filepath.Join(home, ".ssh", "known_hosts")
	}
	check, err := knownhosts.New(file)
	if err != nil {
		return fmt.Errorf("ssh known_hosts: %w", err)
	}
	d.cfg.HostKeyCallback = func(hostname string, remote net.Addr, key ssh.PublicKey) error {
		err := check(hostname, remote, key)
		var ke *knownhosts.KeyError
		if errors.As(err, &ke) {
			if len(ke.Want) == 0 {
				return fmt.Errorf("ssh: host key of %s (%s %s) is not in %s; verify it and add it, e.g. by connecting once with ssh",
					hostname, key.Type(), ssh.FingerprintSHA256(key), file)
			}
			return fmt.Errorf("ssh: HOST KEY MISMATCH for %s (got %s %s, see %s); someone may be intercepting the connection",
				hostname, key.Type(), ssh.FingerprintSHA256(key), file)
		}
		return err
	}

	// Probe with a throwaway key to learn which keys known_hosts lists for this host.
	pub, _, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return err
	}
	probe, err := ssh.NewPublicKey(pub)
	if err != nil {
		return err
	}
	var ke *knownhosts.KeyError
	tcpAddr, _ := net.ResolveTCPAddr("tcp", d.addr)
	if tcpAddr == nil {
		tcpAddr = &net.TCPAddr{}
	}
	if errors.As(check(knownhosts.Normalize(d.addr), tcpAddr, probe), &ke) {
		seen := map[string]bool{}
		for _, k := range ke.Want {
			for _, algo := range hostKeyAlgorithms(k.Key.Type()) {
				if !seen[algo] {
					seen[algo] = true
					d.cfg.HostKeyAlgorithms = append(d.cfg.HostKeyAlgorithms, algo)
				}
			}
		}
	}
	return nil
}

func hostKeyAlgorithms(keyType string) []string {
	switch keyType {
	case ssh.KeyAlgoRSA:
		return []string{ssh.KeyAlgoRSASHA512, ssh.KeyAlgoRSASHA256, ssh.KeyAlgoRSA}
	case ssh.CertAlgoRSAv01:
		return []string{ssh.CertAlgoRSASHA512v01, ssh.CertAlgoRSASHA256v01, ssh.CertAlgoRSAv01}
	default:
		return []string{keyType}
	}
}

// DialContext opens a connection to the remote Docker socket. It matches the
// http.Transport.DialContext signature; network and address are ignored.
func (d *sshDialer) DialContext(ctx context.Context, _, _ string) (net.Conn, error) {
	c, err := d.connect(ctx)
	if err != nil {
		return nil, err
	}
	conn, err := c.DialContext(ctx, "unix", d.socket)
	var open *ssh.OpenChannelError
	if errors.As(err, &open) && open.Reason == ssh.ConnectionFailed {
		return nil, fmt.Errorf("ssh: %s refused to open %s (%w); it must exist and be accessible to the ssh user, "+
			"and sshd needs AllowTcpForwarding and AllowStreamLocalForwarding enabled", d.addr, d.socket, err)
	}
	if err != nil {
		return nil, fmt.Errorf("ssh: open remote socket %s on %s: %w", d.socket, d.addr, err)
	}
	return conn, nil
}

// connect returns the shared SSH connection, establishing it if needed.
func (d *sshDialer) connect(ctx context.Context) (*ssh.Client, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.closed {
		return nil, errors.New("ssh: dialer closed")
	}
	if d.client != nil {
		return d.client, nil
	}

	nd := net.Dialer{Timeout: sshConnectTimeout}
	conn, err := nd.DialContext(ctx, "tcp", d.addr)
	if err != nil {
		return nil, fmt.Errorf("ssh: %w", err)
	}
	// The handshake does not take a context; bound it with a deadline instead.
	deadline := time.Now().Add(sshConnectTimeout)
	if dl, ok := ctx.Deadline(); ok && dl.Before(deadline) {
		deadline = dl
	}
	_ = conn.SetDeadline(deadline)
	cc, chans, reqs, err := ssh.NewClientConn(conn, d.addr, d.cfg)
	if err != nil {
		conn.Close()
		return nil, fmt.Errorf("ssh %s@%s: %w", d.cfg.User, d.addr, err)
	}
	_ = conn.SetDeadline(time.Time{})
	client := ssh.NewClient(cc, chans, reqs)
	d.client = client

	go d.keepalive(client)
	go func() {
		_ = client.Wait()
		d.mu.Lock()
		if d.client == client {
			d.client = nil
		}
		d.mu.Unlock()
	}()
	return client, nil
}

// keepalive detects dead connections; closing the client fails in-flight requests and
// event streams, which makes the hub reconnect.
func (d *sshDialer) keepalive(c *ssh.Client) {
	t := time.NewTicker(sshKeepalive)
	defer t.Stop()
	for range t.C {
		res := make(chan error, 1)
		go func() {
			_, _, err := c.SendRequest("keepalive@openssh.com", true, nil)
			res <- err
		}()
		select {
		case err := <-res:
			if err != nil {
				c.Close()
				return
			}
		case <-time.After(sshKeepalive):
			slog.Warn("ssh: keepalive timed out, closing connection", "addr", d.addr)
			c.Close()
			return
		}
	}
}

func (d *sshDialer) closeAgent() {
	if d.agent != nil {
		d.agent.Close()
	}
}

// Close tears down the SSH connection.
func (d *sshDialer) Close() error {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.closed = true
	d.closeAgent()
	if d.client != nil {
		err := d.client.Close()
		d.client = nil
		return err
	}
	return nil
}
