// Package integration runs dviz in-process against real Docker daemons.
//
// The tests need access to the local Docker socket (unix:///var/run/docker.sock, or
// DOCKER_HOST) and skip otherwise. Multi-host and TLS tests start a privileged docker:dind
// container as a second, tcp+TLS host.
package integration

import (
	"archive/tar"
	"bufio"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"testing/fstest"
	"time"

	"github.com/flbraun/dviz/internal/app"
	"github.com/flbraun/dviz/internal/model"
	"github.com/moby/moby/api/types/container"
	"github.com/moby/moby/api/types/network"
	"github.com/moby/moby/client"
)

const (
	testImage = "busybox:1.37"
	dindImage = "docker:29-dind"
	// labelRun marks every fixture so cleanup can find it.
	labelRun = "dviz.test.run"
)

var (
	runID = func() string {
		b := make([]byte, 4)
		_, _ = rand.Read(b)
		return hex.EncodeToString(b)
	}()

	dockerOnce sync.Once
	dockerCli  *client.Client
	dockerErr  error
)

// daemon returns a raw client for creating fixtures, skipping the test without a daemon.
func daemon(t *testing.T) *client.Client {
	t.Helper()
	dockerOnce.Do(func() {
		dockerCli, dockerErr = client.New(client.FromEnv, client.WithAPIVersionNegotiation())
		if dockerErr != nil {
			return
		}
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if _, dockerErr = dockerCli.Ping(ctx, client.PingOptions{}); dockerErr != nil {
			return
		}
		dockerErr = pull(context.Background(), dockerCli, testImage)
	})
	if dockerErr != nil {
		t.Skipf("docker daemon not available: %v", dockerErr)
	}
	return dockerCli
}

func pull(ctx context.Context, c *client.Client, ref string) error {
	if _, err := c.ImageInspect(ctx, ref); err == nil {
		return nil
	}
	rc, err := c.ImagePull(ctx, ref, client.ImagePullOptions{})
	if err != nil {
		return fmt.Errorf("pull %s: %w", ref, err)
	}
	defer rc.Close()
	return rc.Wait(ctx)
}

// name returns a unique fixture name.
func name(t *testing.T, s string) string {
	t.Helper()
	return fmt.Sprintf("dvizt-%s-%s", runID, s)
}

func labels(extra map[string]string) map[string]string {
	m := map[string]string{labelRun: runID}
	for k, v := range extra {
		m[k] = v
	}
	return m
}

// ctr describes a fixture container.
type ctr struct {
	Name     string
	Cmd      []string
	Env      []string
	Labels   map[string]string
	Networks map[string]*network.EndpointSettings
	Host     *container.HostConfig
	Tty      bool
	Health   *container.HealthConfig
	NoStart  bool
	Image    string
}

// run creates and starts a container and registers its removal.
func run(t *testing.T, c *client.Client, spec ctr) string {
	t.Helper()
	ctx := context.Background()
	if spec.Cmd == nil {
		spec.Cmd = []string{"sleep", "3600"}
	}
	if spec.Image == "" {
		spec.Image = testImage
	}
	hc := spec.Host
	if hc == nil {
		hc = &container.HostConfig{}
	}
	var nc *network.NetworkingConfig
	if len(spec.Networks) > 0 {
		nc = &network.NetworkingConfig{EndpointsConfig: spec.Networks}
		if hc.NetworkMode == "" {
			for n := range spec.Networks {
				hc.NetworkMode = container.NetworkMode(n)
				break
			}
		}
	}
	res, err := c.ContainerCreate(ctx, client.ContainerCreateOptions{
		Name:             spec.Name,
		Config:           &container.Config{Image: spec.Image, Cmd: spec.Cmd, Env: spec.Env, Labels: labels(spec.Labels), Tty: spec.Tty, Healthcheck: spec.Health},
		HostConfig:       hc,
		NetworkingConfig: nc,
	})
	if err != nil {
		t.Fatalf("create %s: %v", spec.Name, err)
	}
	t.Cleanup(func() {
		_, _ = c.ContainerRemove(context.Background(), res.ID, client.ContainerRemoveOptions{Force: true, RemoveVolumes: true})
	})
	if !spec.NoStart {
		if _, err := c.ContainerStart(ctx, res.ID, client.ContainerStartOptions{}); err != nil {
			t.Fatalf("start %s: %v", spec.Name, err)
		}
	}
	return res.ID
}

// netw creates a bridge network and registers its removal (after containers).
func netw(t *testing.T, c *client.Client, n string, opts map[string]string) string {
	t.Helper()
	res, err := c.NetworkCreate(context.Background(), n, client.NetworkCreateOptions{Driver: "bridge", Options: opts, Labels: labels(nil)})
	if err != nil {
		t.Fatalf("network %s: %v", n, err)
	}
	t.Cleanup(func() { _, _ = c.NetworkRemove(context.Background(), res.ID, client.NetworkRemoveOptions{}) })
	return res.ID
}

// instance is an in-process dviz.
type instance struct {
	t          *testing.T
	candidates []string // cwd, home, etc
	mu         sync.Mutex
	addrs      []string
	cancel     context.CancelFunc
	done       chan error
}

// start runs dviz with the given dviz.yml content in a temporary "cwd". The home and /etc
// candidates point at (initially absent) files in temporary directories.
func start(t *testing.T, yaml string) *instance {
	t.Helper()
	root := t.TempDir()
	in := &instance{t: t, candidates: []string{
		filepath.Join(root, "cwd", "dviz.yml"),
		filepath.Join(root, "home", ".config", "dviz", "dviz.yml"),
		filepath.Join(root, "etc", "dviz", "dviz.yml"),
	}}
	for _, c := range in.candidates {
		if err := os.MkdirAll(filepath.Dir(c), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if yaml != "" {
		in.write(0, yaml)
	}
	in.launch()
	return in
}

func (in *instance) launch() {
	ctx, cancel := context.WithCancel(context.Background())
	in.cancel, in.done = cancel, make(chan error, 1)
	listening := make(chan struct{}, 1)
	go func() {
		in.done <- app.Run(ctx, app.Options{
			Candidates: in.candidates,
			Assets:     fstest.MapFS{"index.html": {Data: []byte("<!doctype html><title>dviz</title>")}},
			OnListen: func(addr string) {
				in.mu.Lock()
				in.addrs = append(in.addrs, addr)
				in.mu.Unlock()
				select {
				case listening <- struct{}{}:
				default:
				}
			},
		})
	}()
	select {
	case <-listening:
	case err := <-in.done:
		in.t.Fatalf("dviz exited: %v", err)
	case <-time.After(10 * time.Second):
		in.t.Fatal("dviz did not start listening")
	}
	in.t.Cleanup(func() {
		cancel()
		select {
		case <-in.done:
		case <-time.After(10 * time.Second):
			in.t.Error("dviz did not shut down")
		}
	})
}

// write replaces the candidate file at index i (0 = cwd, 1 = home, 2 = etc) atomically.
func (in *instance) write(i int, yaml string) {
	in.t.Helper()
	p := in.candidates[i]
	tmp := p + ".tmp"
	if err := os.WriteFile(tmp, []byte(yaml), 0o644); err != nil {
		in.t.Fatal(err)
	}
	if err := os.Rename(tmp, p); err != nil {
		in.t.Fatal(err)
	}
}

func (in *instance) base() string {
	in.mu.Lock()
	defer in.mu.Unlock()
	return "http://" + in.addrs[len(in.addrs)-1]
}

func (in *instance) get(path string, v any) int {
	in.t.Helper()
	res, err := http.Get(in.base() + path)
	if err != nil {
		in.t.Fatalf("GET %s: %v", path, err)
	}
	defer res.Body.Close()
	body, _ := io.ReadAll(res.Body)
	if v != nil && res.StatusCode == http.StatusOK {
		if err := json.Unmarshal(body, v); err != nil {
			in.t.Fatalf("GET %s: decode: %v\n%s", path, err, body)
		}
	}
	return res.StatusCode
}

func (in *instance) raw(path string) string {
	in.t.Helper()
	res, err := http.Get(in.base() + path)
	if err != nil {
		in.t.Fatalf("GET %s: %v", path, err)
	}
	defer res.Body.Close()
	body, _ := io.ReadAll(res.Body)
	return string(body)
}

type hostInfo struct {
	Name        string `json:"name"`
	DisplayName string `json:"displayName"`
	Status      string `json:"status"`
	Error       string `json:"error"`
}

func (in *instance) hosts() []hostInfo {
	var hs []hostInfo
	in.get("/api/hosts", &hs)
	return hs
}

func (in *instance) graph(host string) model.Graph {
	var g model.Graph
	if code := in.get("/api/hosts/"+host+"/graph", &g); code != http.StatusOK {
		in.t.Fatalf("graph %s: status %d", host, code)
	}
	return g
}

// eventually polls cond until it returns "" (success) or the timeout expires.
func eventually(t *testing.T, timeout time.Duration, cond func() string) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	var last string
	for time.Now().Before(deadline) {
		if last = cond(); last == "" {
			return
		}
		time.Sleep(200 * time.Millisecond)
	}
	t.Fatalf("condition not met within %s: %s", timeout, last)
}

func (in *instance) waitConnected(names ...string) {
	in.t.Helper()
	eventually(in.t, 30*time.Second, func() string {
		st := map[string]string{}
		for _, h := range in.hosts() {
			st[h.Name] = h.Status + " " + h.Error
		}
		for _, n := range names {
			if !strings.HasPrefix(st[n], "connected") {
				return fmt.Sprintf("host %s: %q", n, st[n])
			}
		}
		return ""
	})
}

// waitGraph polls a host's graph until cond returns "".
func (in *instance) waitGraph(host string, cond func(g model.Graph) string) model.Graph {
	in.t.Helper()
	var g model.Graph
	eventually(in.t, 20*time.Second, func() string {
		g = in.graph(host)
		return cond(g)
	})
	return g
}

func hasNode(g model.Graph, id string) bool {
	_, ok := g.Node(id)
	return ok
}

func findLink(g model.Graph, a, kind, b string) *model.Link {
	for i, l := range g.Links {
		if string(l.Kind) == kind && ((l.Source == a && l.Target == b) || (l.Source == b && l.Target == a)) {
			return &g.Links[i]
		}
	}
	return nil
}

func entityPath(host, id string) string {
	return "/api/hosts/" + host + "/entities/" + url.PathEscape(id)
}

// sseEvent is one server-sent event.
type sseEvent struct {
	Event string
	Data  string
}

// stream opens an SSE endpoint and delivers its events until the test ends.
func (in *instance) stream(path string) <-chan sseEvent {
	in.t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	in.t.Cleanup(cancel)
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, in.base()+path, nil)
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		in.t.Fatalf("stream %s: %v", path, err)
	}
	if res.StatusCode != http.StatusOK {
		in.t.Fatalf("stream %s: status %d", path, res.StatusCode)
	}
	out := make(chan sseEvent, 1024)
	go func() {
		defer res.Body.Close()
		defer close(out)
		sc := bufio.NewScanner(res.Body)
		sc.Buffer(make([]byte, 1<<20), 16<<20)
		var ev sseEvent
		for sc.Scan() {
			line := sc.Text()
			switch {
			case strings.HasPrefix(line, "event: "):
				ev.Event = strings.TrimPrefix(line, "event: ")
			case strings.HasPrefix(line, "data: "):
				ev.Data = strings.TrimPrefix(line, "data: ")
			case line == "" && ev.Event != "":
				select {
				case out <- ev:
				case <-ctx.Done():
					return
				}
				ev = sseEvent{}
			}
		}
	}()
	return out
}

// next waits for the next event matching pred.
func next(t *testing.T, ch <-chan sseEvent, timeout time.Duration, pred func(sseEvent) bool) sseEvent {
	t.Helper()
	deadline := time.After(timeout)
	for {
		select {
		case ev, ok := <-ch:
			if !ok {
				t.Fatal("stream closed")
			}
			if pred(ev) {
				return ev
			}
		case <-deadline:
			t.Fatalf("no matching event within %s", timeout)
		}
	}
}

// dindHost is a docker:dind daemon reachable over tcp+TLS.
type dindHost struct {
	URL     string
	CertDir string
	Client  *client.Client
}

var (
	dindOnce sync.Once
	dindVal  *dindHost
	dindErr  error
)

// dind returns a shared docker:dind host with swarm mode enabled. It is removed when the
// test binary exits (see TestMain).
func dind(t *testing.T) *dindHost {
	t.Helper()
	c := daemon(t)
	dindOnce.Do(func() { dindVal, dindErr = startDind(c) })
	if dindErr != nil {
		t.Fatalf("dind: %v", dindErr)
	}
	return dindVal
}

var dindContainer string

func startDind(c *client.Client) (*dindHost, error) {
	ctx := context.Background()
	if err := pull(ctx, c, dindImage); err != nil {
		return nil, err
	}
	port := network.MustParsePort("2376/tcp")
	res, err := c.ContainerCreate(ctx, client.ContainerCreateOptions{
		Name:   fmt.Sprintf("dvizt-%s-dind", runID),
		Config: &container.Config{Image: dindImage, Env: []string{"DOCKER_TLS_CERTDIR=/certs"}, Labels: labels(nil), ExposedPorts: network.PortSet{port: {}}},
		HostConfig: &container.HostConfig{
			Privileged:   true,
			PortBindings: network.PortMap{port: {{HostIP: mustAddr("127.0.0.1")}}},
		},
	})
	if err != nil {
		return nil, err
	}
	dindContainer = res.ID
	if _, err := c.ContainerStart(ctx, res.ID, client.ContainerStartOptions{}); err != nil {
		return nil, err
	}
	insp, err := c.ContainerInspect(ctx, res.ID, client.ContainerInspectOptions{})
	if err != nil {
		return nil, err
	}
	bindings := insp.Container.NetworkSettings.Ports[port]
	if len(bindings) == 0 {
		return nil, errors.New("dind port not published")
	}
	h := &dindHost{URL: "tcp://127.0.0.1:" + bindings[0].HostPort}
	if h.CertDir, err = os.MkdirTemp("", "dvizt-dind-certs-"); err != nil {
		return nil, err
	}

	deadline := time.Now().Add(90 * time.Second)
	for {
		if time.Now().After(deadline) {
			return nil, fmt.Errorf("dind not ready: %v", err)
		}
		time.Sleep(time.Second)
		if err = copyCerts(ctx, c, res.ID, h.CertDir); err != nil {
			continue
		}
		h.Client, err = client.New(
			client.WithHost(h.URL),
			client.WithTLSClientConfig(filepath.Join(h.CertDir, "ca.pem"), filepath.Join(h.CertDir, "cert.pem"), filepath.Join(h.CertDir, "key.pem")),
			client.WithAPIVersionNegotiation(),
		)
		if err != nil {
			continue
		}
		pctx, cancel := context.WithTimeout(ctx, 3*time.Second)
		_, err = h.Client.Ping(pctx, client.PingOptions{})
		cancel()
		if err == nil {
			break
		}
	}
	if _, err := h.Client.SwarmInit(ctx, client.SwarmInitOptions{ListenAddr: "0.0.0.0:2377", AdvertiseAddr: "eth0"}); err != nil {
		return nil, fmt.Errorf("swarm init: %w", err)
	}
	return h, nil
}

func copyCerts(ctx context.Context, c *client.Client, id, dir string) error {
	res, err := c.CopyFromContainer(ctx, id, client.CopyFromContainerOptions{SourcePath: "/certs/client"})
	if err != nil {
		return err
	}
	defer res.Content.Close()
	tr := tar.NewReader(res.Content)
	found := 0
	for {
		hdr, err := tr.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return err
		}
		base := filepath.Base(hdr.Name)
		if base != "ca.pem" && base != "cert.pem" && base != "key.pem" {
			continue
		}
		data, err := io.ReadAll(tr)
		if err != nil {
			return err
		}
		if err := os.WriteFile(filepath.Join(dir, base), data, 0o600); err != nil {
			return err
		}
		found++
	}
	if found != 3 {
		return errors.New("client certs not generated yet")
	}
	return nil
}

func TestMain(m *testing.M) {
	code := m.Run()
	if dindContainer != "" && dockerCli != nil {
		_, _ = dockerCli.ContainerRemove(context.Background(), dindContainer, client.ContainerRemoveOptions{Force: true, RemoveVolumes: true})
	}
	if sshdContainer != "" && dockerCli != nil {
		_, _ = dockerCli.ContainerRemove(context.Background(), sshdContainer, client.ContainerRemoveOptions{Force: true, RemoveVolumes: true})
	}
	if sshdVal != nil {
		_ = os.RemoveAll(sshdVal.Dir)
	}
	if dindVal != nil {
		_ = os.RemoveAll(dindVal.CertDir)
	}
	os.Exit(code)
}

// localYAML is a config with only the local daemon on a random port.
func localYAML() string {
	return fmt.Sprintf("listen: 127.0.0.1:0\nhosts:\n  - name: local\n    url: %s\n", localURL())
}

// localURL is the local daemon's socket: $DOCKER_HOST or the default socket.
func localURL() string {
	if host := os.Getenv("DOCKER_HOST"); host != "" {
		return host
	}
	return "unix:///var/run/docker.sock"
}

func dindYAML(h *dindHost, display string) string {
	return fmt.Sprintf(`  - name: dind
    display_name: %q
    url: %s
    tls:
      ca: %s
      cert: %s
      key: %s
`, display, h.URL, filepath.Join(h.CertDir, "ca.pem"), filepath.Join(h.CertDir, "cert.pem"), filepath.Join(h.CertDir, "key.pem"))
}
