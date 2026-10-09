package integration

import (
	"context"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/flbraun/dviz/internal/app"
	"github.com/flbraun/dviz/internal/config"
	"github.com/flbraun/dviz/internal/docker"
	"github.com/flbraun/dviz/internal/model"
)

func TestMultipleHosts(t *testing.T) {
	c := daemon(t)
	dh := dind(t)
	local := run(t, c, ctr{Name: name(t, "multi")})

	in := start(t, localYAML()+dindYAML(dh, "Docker in Docker")+"  - name: gone\n    display_name: Unreachable\n    url: tcp://127.0.0.1:1\n")
	in.waitConnected("local", "dind")

	byName := map[string]hostInfo{}
	for _, h := range in.hosts() {
		byName[h.Name] = h
	}
	if got := byName["dind"].DisplayName; got != "Docker in Docker" {
		t.Errorf("dind display name = %q", got)
	}
	if got := byName["local"].DisplayName; got != "local" {
		t.Errorf("local display name defaults to name, got %q", got)
	}
	eventually(t, 15*time.Second, func() string {
		for _, h := range in.hosts() {
			if h.Name == "gone" && h.Status == "error" && h.Error != "" {
				return ""
			}
		}
		return "unreachable host not reported as error"
	})
	if g := in.graph("gone"); len(g.Nodes) != 1 || g.Nodes[0].Status != "error" {
		t.Errorf("unreachable host graph = %+v", g)
	}

	// Scenes are separate: the local fixture is not part of the dind graph.
	in.waitGraph("local", func(g model.Graph) string {
		if !hasNode(g, docker.ContainerID(local)) {
			return "local fixture missing"
		}
		return ""
	})
	if hasNode(in.graph("dind"), docker.ContainerID(local)) {
		t.Error("local container appears in the dind graph")
	}
	if code := in.get("/api/hosts/nope/graph", nil); code != http.StatusNotFound {
		t.Errorf("unknown host: status %d", code)
	}
}

func TestConfigReload(t *testing.T) {
	daemon(t)
	dh := dind(t)
	in := start(t, localYAML())
	in.waitConnected("local")
	names := func() string {
		var out []string
		for _, h := range in.hosts() {
			out = append(out, h.Name+"="+h.DisplayName)
		}
		return strings.Join(out, ",")
	}

	in.write(0, localYAML()+dindYAML(dh, "first"))
	in.waitConnected("local", "dind")
	if got := names(); got != "local=local,dind=first" {
		t.Errorf("after adding dind: %s", got)
	}

	in.write(0, localYAML()+dindYAML(dh, "renamed"))
	eventually(t, 10*time.Second, func() string {
		if got := names(); got != "local=local,dind=renamed" {
			return got
		}
		return ""
	})

	// An invalid file is ignored and the running config stays.
	in.write(0, "listen: 127.0.0.1:0\nhosts:\n  - name: Not Valid\n    url: ftp://x\n")
	time.Sleep(1500 * time.Millisecond)
	if got := names(); got != "local=local,dind=renamed" {
		t.Errorf("invalid config was applied: %s", got)
	}

	in.write(0, localYAML())
	eventually(t, 10*time.Second, func() string {
		if got := names(); got != "local=local" {
			return got
		}
		return ""
	})
	if code := in.get("/api/hosts/dind/graph", nil); code != http.StatusNotFound {
		t.Errorf("removed host graph: status %d", code)
	}

	// Changing the listen address moves the server.
	old := in.base()
	in.write(0, strings.Replace(localYAML(), "listen: 127.0.0.1:0", "listen: 127.0.0.2:0", 1))
	eventually(t, 10*time.Second, func() string {
		if in.base() == old {
			return "still on " + old
		}
		return ""
	})
	if !strings.HasPrefix(in.base(), "http://127.0.0.2:") {
		t.Errorf("new base = %s", in.base())
	}
	in.waitConnected("local")
	if _, err := http.Get(old + "/api/hosts"); err == nil {
		t.Errorf("old listener %s still serves", old)
	}
}

func TestConfigResolutionOrder(t *testing.T) {
	daemon(t)
	withDisplay := func(d string) string {
		return strings.Replace(localYAML(), "    url:", "    display_name: "+d+"\n    url:", 1)
	}
	in := &instance{t: t}
	root := t.TempDir()
	in.candidates = []string{root + "/cwd/dviz.yml", root + "/home/.config/dviz/dviz.yml", root + "/etc/dviz/dviz.yml"}
	for _, c := range in.candidates {
		if err := os.MkdirAll(c[:strings.LastIndex(c, "/")], 0o755); err != nil {
			t.Fatal(err)
		}
	}
	in.write(2, withDisplay("from-etc"))
	in.write(1, withDisplay("from-home"))
	in.launch()

	display := func() string {
		hs := in.hosts()
		if len(hs) != 1 {
			return ""
		}
		return hs[0].DisplayName
	}
	if got := display(); got != "from-home" {
		t.Fatalf("home must win over etc, got %q", got)
	}
	in.write(0, withDisplay("from-cwd"))
	eventually(t, 10*time.Second, func() string {
		if got := display(); got != "from-cwd" {
			return got
		}
		return ""
	})
	if err := os.Remove(in.candidates[0]); err != nil {
		t.Fatal(err)
	}
	eventually(t, 10*time.Second, func() string {
		if got := display(); got != "from-home" {
			return got
		}
		return ""
	})
	if err := os.Remove(in.candidates[1]); err != nil {
		t.Fatal(err)
	}
	eventually(t, 10*time.Second, func() string {
		if got := display(); got != "from-etc" {
			return got
		}
		return ""
	})
}

func TestConfigRequired(t *testing.T) {
	daemon(t)
	root := t.TempDir()
	candidates := []string{filepath.Join(root, "a", "dviz.yml"), filepath.Join(root, "b", "dviz.yml")}
	err := app.Run(context.Background(), app.Options{Candidates: candidates})
	if !errors.Is(err, config.ErrNoConfig) || !strings.Contains(err.Error(), candidates[1]) {
		t.Fatalf("start without config: err = %v", err)
	}

	// Deleting the only config while running keeps the running config.
	in := start(t, localYAML())
	in.waitConnected("local")
	if err := os.Remove(in.candidates[0]); err != nil {
		t.Fatal(err)
	}
	time.Sleep(1500 * time.Millisecond)
	if hs := in.hosts(); len(hs) != 1 || hs[0].Name != "local" {
		t.Errorf("hosts after deleting the config = %+v", hs)
	}
}

func TestOpenBrowser(t *testing.T) {
	daemon(t)
	opened := func(in *instance) []string {
		in.mu.Lock()
		defer in.mu.Unlock()
		return append([]string(nil), in.opened...)
	}

	in := start(t, localYAML())
	if got := opened(in); len(got) != 1 || got[0] != in.base()+"/" {
		t.Fatalf("headless unset: opened %v, want [%s/]", got, in.base())
	}
	// Reloads, including a listen change, never open another tab.
	in.write(0, strings.Replace(localYAML(), "listen: 127.0.0.1:0", "listen: 127.0.0.2:0", 1))
	eventually(t, 10*time.Second, func() string {
		if !strings.HasPrefix(in.base(), "http://127.0.0.2:") {
			return "listener not moved"
		}
		return ""
	})
	if got := opened(in); len(got) != 1 {
		t.Errorf("reload opened another tab: %v", got)
	}

	headless := start(t, "headless: true\n"+localYAML())
	if got := opened(headless); len(got) != 0 {
		t.Errorf("headless: true opened %v", got)
	}
}

func TestHostHeaderGuard(t *testing.T) {
	daemon(t)
	in := start(t, localYAML())
	for host, want := range map[string]int{
		"evil.example":     http.StatusForbidden,
		"localhost":        http.StatusOK,
		"127.0.0.1":        http.StatusOK,
		"dviz.localhost":   http.StatusOK,
		"127.0.0.1.nip.io": http.StatusForbidden,
	} {
		req, _ := http.NewRequest(http.MethodGet, in.base()+"/api/hosts", nil)
		req.Host = host
		res, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		res.Body.Close()
		if res.StatusCode != want {
			t.Errorf("Host %q: status %d, want %d", host, res.StatusCode, want)
		}
	}
	// The API is read-only.
	res, err := http.Post(in.base()+"/api/hosts", "application/json", nil)
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	if res.StatusCode != http.StatusMethodNotAllowed {
		t.Errorf("POST: status %d", res.StatusCode)
	}
}
