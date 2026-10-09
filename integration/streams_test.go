package integration

import (
	"encoding/json"
	"slices"
	"testing"
	"time"

	"github.com/flbraun/dviz/internal/docker"
	"github.com/flbraun/dviz/internal/model"
)

func TestLogs(t *testing.T) {
	c := daemon(t)
	script := []string{"sh", "-c", "echo out-1; echo err-1 >&2; echo out-2; sleep 3600"}
	plain := run(t, c, ctr{Name: name(t, "logs"), Cmd: script})
	tty := run(t, c, ctr{Name: name(t, "logs-tty"), Cmd: script, Tty: true})

	in := start(t, localYAML())
	in.waitConnected("local")
	in.waitGraph("local", func(g model.Graph) string {
		if !hasNode(g, docker.ContainerID(plain)) || !hasNode(g, docker.ContainerID(tty)) {
			return "fixtures missing"
		}
		return ""
	})

	read := func(id string) []docker.LogLine {
		t.Helper()
		var lines []docker.LogLine
		ev := in.stream("/api/hosts/local/containers/" + id + "/logs?tail=100&follow=0")
		for {
			e := next(t, ev, 10*time.Second, func(sseEvent) bool { return true })
			switch e.Event {
			case "line":
				var l docker.LogLine
				_ = json.Unmarshal([]byte(e.Data), &l)
				lines = append(lines, l)
			case "error":
				t.Fatalf("logs error: %s", e.Data)
			case "eof":
				return lines
			}
		}
	}

	eventually(t, 10*time.Second, func() string {
		if len(read(plain)) < 3 {
			return "waiting for output"
		}
		return ""
	})
	lines := read(plain)
	want := []docker.LogLine{{Stream: "stdout", Text: "out-1"}, {Stream: "stderr", Text: "err-1"}, {Stream: "stdout", Text: "out-2"}}
	for _, w := range want {
		if !slices.ContainsFunc(lines, func(l docker.LogLine) bool { return l.Stream == w.Stream && l.Text == w.Text && l.TS != "" }) {
			t.Errorf("non-TTY logs %+v lack %+v", lines, w)
		}
	}
	// With a TTY, Docker merges streams: everything is stdout.
	ttyLines := read(tty)
	for _, w := range []string{"out-1", "err-1", "out-2"} {
		if !slices.ContainsFunc(ttyLines, func(l docker.LogLine) bool { return l.Stream == "stdout" && l.Text == w }) {
			t.Errorf("TTY logs %+v lack %q", ttyLines, w)
		}
	}

	if code := in.get("/api/hosts/local/containers/not-a-container/logs", nil); code != 404 {
		t.Errorf("unknown container logs: status %d", code)
	}
}

func TestStats(t *testing.T) {
	c := daemon(t)
	id := run(t, c, ctr{Name: name(t, "stats")})
	in := start(t, localYAML())
	in.waitConnected("local")
	in.waitGraph("local", func(g model.Graph) string {
		if !hasNode(g, docker.ContainerID(id)) {
			return "fixture missing"
		}
		return ""
	})
	ev := in.stream("/api/hosts/local/containers/" + id + "/stats")
	for i := 0; i < 2; i++ {
		e := next(t, ev, 10*time.Second, func(e sseEvent) bool { return e.Event == "sample" || e.Event == "error" })
		if e.Event == "error" {
			t.Fatalf("stats error: %s", e.Data)
		}
		var s docker.StatsSample
		if err := json.Unmarshal([]byte(e.Data), &s); err != nil {
			t.Fatal(err)
		}
		if s.MemLimit == 0 || s.MemUsage == 0 || s.PIDs == 0 || s.TS == 0 {
			t.Errorf("sample %d = %+v", i, s)
		}
	}
}
