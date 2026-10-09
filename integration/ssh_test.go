package integration

import (
	"archive/tar"
	"bufio"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/flbraun/dviz/internal/app"
	"github.com/flbraun/dviz/internal/docker"
	"github.com/flbraun/dviz/internal/model"
	"github.com/moby/moby/api/types/container"
	"github.com/moby/moby/api/types/mount"
	"github.com/moby/moby/api/types/network"
	"github.com/moby/moby/client"
	"golang.org/x/crypto/ssh"
	"golang.org/x/crypto/ssh/agent"
	"golang.org/x/crypto/ssh/knownhosts"
)

const sshdImage = "alpine:3.22"

// sshdHost is an OpenSSH server whose /var/run/docker.sock is the local daemon's socket.
type sshdHost struct {
	Addr          string // 127.0.0.1:port
	Key           ed25519.PrivateKey
	KeyFile       string // authorized private key (OpenSSH PEM)
	ProtectedFile string // authorized, passphrase-protected private key
	Passphrase    string
	Password      string // root's login password
	KnownHosts    string // known_hosts listing the server's host key
	Dir           string
}

var (
	sshdOnce      sync.Once
	sshdVal       *sshdHost
	sshdErr       error
	sshdContainer string
)

func sshd(t *testing.T) *sshdHost {
	t.Helper()
	c := daemon(t)
	sshdOnce.Do(func() { sshdVal, sshdErr = startSSHD(c) })
	if sshdErr != nil {
		t.Fatalf("sshd: %v", sshdErr)
	}
	return sshdVal
}

func startSSHD(c *client.Client) (*sshdHost, error) {
	ctx := context.Background()
	if err := pull(ctx, c, sshdImage); err != nil {
		return nil, err
	}
	dir, err := os.MkdirTemp("", "dvizt-sshd-")
	if err != nil {
		return nil, err
	}
	h := &sshdHost{Dir: dir}
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return nil, err
	}
	h.Key = priv
	if h.KeyFile, err = writeKey(dir, "id_ed25519", priv); err != nil {
		return nil, err
	}
	sshPub, err := ssh.NewPublicKey(pub)
	if err != nil {
		return nil, err
	}
	ppub, ppriv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return nil, err
	}
	h.Passphrase = "passphrase-" + runID
	block, err := ssh.MarshalPrivateKeyWithPassphrase(ppriv, "", []byte(h.Passphrase))
	if err != nil {
		return nil, err
	}
	h.ProtectedFile = filepath.Join(dir, "id_protected")
	if err := os.WriteFile(h.ProtectedFile, pem.EncodeToMemory(block), 0o600); err != nil {
		return nil, err
	}
	sshPPub, err := ssh.NewPublicKey(ppub)
	if err != nil {
		return nil, err
	}
	h.Password = "pw-" + runID
	authorized := strings.TrimSpace(string(ssh.MarshalAuthorizedKey(sshPub))) + "\n" + strings.TrimSpace(string(ssh.MarshalAuthorizedKey(sshPPub)))

	script := `set -e
apk add --no-cache openssh-server >/dev/null
ssh-keygen -A >/dev/null
echo "root:$ROOT_PASSWORD" | chpasswd
mkdir -p /root/.ssh && chmod 700 /root/.ssh
printf '%s\n' "$AUTHORIZED_KEY" > /root/.ssh/authorized_keys && chmod 600 /root/.ssh/authorized_keys
touch /ready
exec /usr/sbin/sshd -D -e -o PermitRootLogin=yes -o PasswordAuthentication=yes -o AllowStreamLocalForwarding=yes -o AllowTcpForwarding=local -o PerSourcePenalties=no`
	port := network.MustParsePort("22/tcp")
	res, err := c.ContainerCreate(ctx, client.ContainerCreateOptions{
		Name: fmt.Sprintf("dvizt-%s-sshd", runID),
		Config: &container.Config{
			Image: sshdImage, Cmd: []string{"sh", "-c", script}, Labels: labels(nil),
			Env:          []string{"AUTHORIZED_KEY=" + authorized, "ROOT_PASSWORD=" + h.Password},
			ExposedPorts: network.PortSet{port: {}},
		},
		HostConfig: &container.HostConfig{
			PortBindings: network.PortMap{port: {{HostIP: mustAddr("127.0.0.1")}}},
			Mounts:       []mount.Mount{{Type: mount.TypeBind, Source: "/var/run/docker.sock", Target: "/var/run/docker.sock"}},
		},
	})
	if err != nil {
		return nil, err
	}
	sshdContainer = res.ID
	if _, err := c.ContainerStart(ctx, res.ID, client.ContainerStartOptions{}); err != nil {
		return nil, err
	}
	insp, err := c.ContainerInspect(ctx, res.ID, client.ContainerInspectOptions{})
	if err != nil {
		return nil, err
	}
	h.Addr = "127.0.0.1:" + insp.Container.NetworkSettings.Ports[port][0].HostPort

	// Wait for sshd, then record its real host key in a known_hosts file.
	deadline := time.Now().Add(90 * time.Second)
	for {
		if time.Now().After(deadline) {
			return nil, fmt.Errorf("sshd not ready: %v", err)
		}
		time.Sleep(500 * time.Millisecond)
		var hostKey ssh.PublicKey
		if hostKey, err = readHostKey(ctx, c, res.ID); err != nil {
			continue
		}
		if err = banner(h.Addr); err != nil {
			continue
		}
		h.KnownHosts = filepath.Join(dir, "known_hosts")
		line := knownhosts.Line([]string{knownhosts.Normalize(h.Addr)}, hostKey)
		return h, os.WriteFile(h.KnownHosts, []byte(line+"\n"), 0o600)
	}
}

func writeKey(dir, name string, key ed25519.PrivateKey) (string, error) {
	block, err := ssh.MarshalPrivateKey(key, "")
	if err != nil {
		return "", err
	}
	p := filepath.Join(dir, name)
	return p, os.WriteFile(p, pem.EncodeToMemory(block), 0o600)
}

func readHostKey(ctx context.Context, c *client.Client, id string) (ssh.PublicKey, error) {
	if _, err := c.ContainerStatPath(ctx, id, client.ContainerStatPathOptions{Path: "/ready"}); err != nil {
		return nil, err
	}
	res, err := c.CopyFromContainer(ctx, id, client.CopyFromContainerOptions{SourcePath: "/etc/ssh/ssh_host_ed25519_key.pub"})
	if err != nil {
		return nil, err
	}
	defer res.Content.Close()
	tr := tar.NewReader(res.Content)
	if _, err := tr.Next(); err != nil {
		return nil, err
	}
	data, err := io.ReadAll(tr)
	if err != nil {
		return nil, err
	}
	key, _, _, _, err := ssh.ParseAuthorizedKey(data)
	return key, err
}

func banner(addr string) error {
	conn, err := net.DialTimeout("tcp", addr, time.Second)
	if err != nil {
		return err
	}
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(2 * time.Second))
	line, err := bufio.NewReader(conn).ReadString('\n')
	if err != nil {
		return err
	}
	if !strings.HasPrefix(line, "SSH-") {
		return errors.New("no ssh banner")
	}
	return nil
}

func sshYAML(h *sshdHost, sshBlock string) string {
	return fmt.Sprintf("listen: 127.0.0.1:0\nhosts:\n  - name: remote\n    display_name: Remote via SSH\n    url: ssh://root@%s\n    ssh:\n%s      known_hosts: %s\n",
		h.Addr, sshBlock, h.KnownHosts)
}

func secretFile(t *testing.T, content string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "secret")
	if err := os.WriteFile(p, []byte(content+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestSSHHost(t *testing.T) {
	c := daemon(t)
	h := sshd(t)
	fixture := run(t, c, ctr{Name: name(t, "via-ssh"), Cmd: []string{"sh", "-c", "echo over-ssh; sleep 3600"}})

	in := start(t, sshYAML(h, fmt.Sprintf("      method: key\n      identity_file: %s\n", h.KeyFile)))
	in.waitConnected("remote")
	if hs := in.hosts(); hs[0].DisplayName != "Remote via SSH" {
		t.Errorf("hosts = %+v", hs)
	}
	if url := in.raw("/api/hosts"); !strings.Contains(url, `"url":"ssh://root@`+h.Addr+`/var/run/docker.sock"`) {
		t.Errorf("hosts url: %s", url)
	}

	id := docker.ContainerID(fixture)
	in.waitGraph("remote", func(g model.Graph) string {
		if !hasNode(g, id) {
			return "fixture not visible through ssh"
		}
		return ""
	})

	// Streams work through the tunnel too.
	ev := in.stream("/api/hosts/remote/containers/" + fixture + "/logs?tail=10&follow=0")
	line := next(t, ev, 10*time.Second, func(e sseEvent) bool { return e.Event == "line" || e.Event == "error" })
	if line.Event != "line" || !strings.Contains(line.Data, "over-ssh") {
		t.Errorf("logs over ssh: %+v", line)
	}

	// Live updates arrive over the long-lived SSH event stream.
	timeout := 0
	if _, err := c.ContainerStop(context.Background(), fixture, client.ContainerStopOptions{Timeout: &timeout}); err != nil {
		t.Fatal(err)
	}
	in.waitGraph("remote", func(g model.Graph) string {
		if n, _ := g.Node(id); n.Status != "exited" {
			return "status " + n.Status
		}
		return ""
	})
}

// TestSSHMethods connects with each configured authentication method.
func TestSSHMethods(t *testing.T) {
	daemon(t)
	h := sshd(t)

	keyring := agent.NewKeyring()
	if err := keyring.Add(agent.AddedKey{PrivateKey: h.Key}); err != nil {
		t.Fatal(err)
	}
	sock := filepath.Join(t.TempDir(), "agent.sock")
	l, err := net.Listen("unix", sock)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { l.Close() })
	go func() {
		for {
			conn, err := l.Accept()
			if err != nil {
				return
			}
			go func() { _ = agent.ServeAgent(keyring, conn); conn.Close() }()
		}
	}()

	for _, tc := range []struct {
		name, block string
		agentSock   string
	}{
		{"password", "      method: password\n      password: " + secretFile(t, h.Password) + "\n", ""},
		{"agent", "      method: agent\n", sock},
		{"key", "      method: key\n      identity_file: " + h.KeyFile + "\n", ""},
		{"protected key", "      method: key\n      identity_file: " + h.ProtectedFile + "\n      password: " + secretFile(t, h.Passphrase) + "\n", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("SSH_AUTH_SOCK", tc.agentSock)
			in := start(t, sshYAML(h, tc.block))
			in.waitConnected("remote")
		})
	}
}

func TestSSHRejections(t *testing.T) {
	daemon(t)
	h := sshd(t)
	t.Setenv("SSH_AUTH_SOCK", "")

	_, otherKey, _ := ed25519.GenerateKey(rand.Reader)
	otherFile, err := writeKey(t.TempDir(), "other", otherKey)
	if err != nil {
		t.Fatal(err)
	}
	emptyKnownHosts := filepath.Join(t.TempDir(), "known_hosts")
	if err := os.WriteFile(emptyKnownHosts, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	_, wrongHost, _ := ed25519.GenerateKey(rand.Reader)
	wrongPub, _ := ssh.NewPublicKey(wrongHost.Public())
	wrongKnownHosts := filepath.Join(t.TempDir(), "known_hosts")
	if err := os.WriteFile(wrongKnownHosts, []byte(knownhosts.Line([]string{knownhosts.Normalize(h.Addr)}, wrongPub)+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	key := fmt.Sprintf("      method: key\n      identity_file: %s\n", h.KeyFile)

	for _, tc := range []struct {
		name, block, knownHosts, wantErr string
	}{
		{"unknown host key", key, emptyKnownHosts, "is not in"},
		{"host key mismatch", key, wrongKnownHosts, "HOST KEY MISMATCH"},
		{"unauthorized key", "      method: key\n      identity_file: " + otherFile + "\n", h.KnownHosts, "unable to authenticate"},
		{"wrong password", "      method: password\n      password: " + secretFile(t, "nope") + "\n", h.KnownHosts, "unable to authenticate"},
		{"protected key without passphrase", "      method: key\n      identity_file: " + h.ProtectedFile + "\n", h.KnownHosts, "passphrase-protected"},
		{"agent not running", "      method: agent\n", h.KnownHosts, "SSH_AUTH_SOCK is not set"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			yaml := strings.Replace(sshYAML(h, tc.block), "known_hosts: "+h.KnownHosts, "known_hosts: "+tc.knownHosts, 1)
			in := start(t, yaml)
			eventually(t, 15*time.Second, func() string {
				hs := in.hosts()
				if hs[0].Status == "error" && strings.Contains(hs[0].Error, tc.wantErr) {
					return ""
				}
				return fmt.Sprintf("host = %+v", hs[0])
			})
		})
	}
}

// TestSSHConfigValidation checks that incomplete or contradictory ssh settings are
// rejected when the config is loaded.
func TestSSHConfigValidation(t *testing.T) {
	for _, tc := range []struct{ block, wantErr string }{
		{"", "ssh.method is required"},
		{"    ssh:\n      method: magic\n", "must be password, agent or key"},
		{"    ssh:\n      method: password\n", "ssh.password"},
		{"    ssh:\n      method: key\n", "ssh.identity_file is required"},
		{"    ssh:\n      method: agent\n      password: x\n", "not used with method agent"},
		{"    ssh:\n      method: password\n      password: x\n      identity_file: y\n", "only used with method key"},
	} {
		dir := t.TempDir()
		cfg := "hosts:\n  - name: remote\n    url: ssh://root@example.com\n" + tc.block
		if err := os.WriteFile(filepath.Join(dir, "dviz.yml"), []byte(cfg), 0o600); err != nil {
			t.Fatal(err)
		}
		err := app.Run(context.Background(), app.Options{Candidates: []string{filepath.Join(dir, "dviz.yml")}})
		if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
			t.Errorf("config %q: err = %v, want %q", tc.block, err, tc.wantErr)
		}
	}
}
