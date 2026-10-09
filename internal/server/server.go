// Package server exposes the read-only HTTP API and the embedded frontend.
package server

import (
	"encoding/json"
	"errors"
	"io/fs"
	"log/slog"
	"net"
	"net/http"
	"path"
	"strconv"
	"strings"
	"time"

	"github.com/flbraun/dviz/internal/docker"
	"github.com/flbraun/dviz/internal/hub"
	"github.com/flbraun/dviz/internal/model"
)

const keepalive = 15 * time.Second

// Server serves the API for a registry plus the frontend assets.
type Server struct {
	reg    *hub.Registry
	assets fs.FS
}

// New creates a server.
func New(reg *hub.Registry, assets fs.FS) *Server {
	return &Server{reg: reg, assets: assets}
}

// Handler returns the HTTP handler. With loopbackOnly, requests whose Host header is not a
// loopback name are rejected, which defeats DNS rebinding against a localhost listener.
func (s *Server) Handler(loopbackOnly bool) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/hosts", s.hosts)
	mux.HandleFunc("GET /api/events", s.hostEvents)
	mux.HandleFunc("GET /api/hosts/{host}/graph", s.graph)
	mux.HandleFunc("GET /api/hosts/{host}/graph/events", s.graphEvents)
	mux.HandleFunc("GET /api/hosts/{host}/entities/{id}", s.entity)
	mux.HandleFunc("GET /api/hosts/{host}/containers/{id}/stats", s.stats)
	mux.HandleFunc("GET /api/hosts/{host}/containers/{id}/logs", s.logs)
	mux.HandleFunc("/api/", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			httpError(w, http.StatusMethodNotAllowed, "method not allowed")
			return
		}
		httpError(w, http.StatusNotFound, "not found")
	})
	mux.Handle("/", s.static())

	var h http.Handler = mux
	if loopbackOnly {
		h = loopbackGuard(h)
	}
	return h
}

func loopbackGuard(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		host := r.Host
		if h, _, err := net.SplitHostPort(host); err == nil {
			host = h
		}
		host = strings.Trim(host, "[]")
		ip := net.ParseIP(host)
		if host == "localhost" || strings.HasSuffix(host, ".localhost") || (ip != nil && ip.IsLoopback()) {
			next.ServeHTTP(w, r)
			return
		}
		httpError(w, http.StatusForbidden, "forbidden host")
	})
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	if err := json.NewEncoder(w).Encode(v); err != nil {
		slog.Debug("write json", "err", err)
	}
}

func httpError(w http.ResponseWriter, code int, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(map[string]string{"error": msg})
}

func (s *Server) hub(w http.ResponseWriter, r *http.Request) (*hub.Hub, bool) {
	h, ok := s.reg.Get(r.PathValue("host"))
	if !ok {
		httpError(w, http.StatusNotFound, "unknown host")
	}
	return h, ok
}

func (s *Server) hosts(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, s.reg.Hosts())
}

func (s *Server) hostEvents(w http.ResponseWriter, r *http.Request) {
	ch, cancel := s.reg.SubscribeHosts()
	defer cancel()
	sse := newSSE(w)
	if err := sse.send("hosts", s.reg.Hosts()); err != nil {
		return
	}
	tick := time.NewTicker(keepalive)
	defer tick.Stop()
	for {
		select {
		case <-r.Context().Done():
			return
		case <-ch:
			if err := sse.send("hosts", s.reg.Hosts()); err != nil {
				return
			}
		case <-tick.C:
			if err := sse.comment(); err != nil {
				return
			}
		}
	}
}

func (s *Server) graph(w http.ResponseWriter, r *http.Request) {
	h, ok := s.hub(w, r)
	if !ok {
		return
	}
	writeJSON(w, h.Graph())
}

func (s *Server) graphEvents(w http.ResponseWriter, r *http.Request) {
	h, ok := s.hub(w, r)
	if !ok {
		return
	}
	g, diffs, cancel := h.Subscribe()
	defer cancel()
	sse := newSSE(w)
	if err := sse.send("snapshot", g); err != nil {
		return
	}
	tick := time.NewTicker(keepalive)
	defer tick.Stop()
	for {
		select {
		case <-r.Context().Done():
			return
		case d, ok := <-diffs:
			if !ok {
				return // dropped or host removed; the client reconnects
			}
			if err := sse.send("diff", d); err != nil {
				return
			}
		case <-tick.C:
			if err := sse.comment(); err != nil {
				return
			}
		}
	}
}

// Related is a node linked to the requested entity.
type Related struct {
	model.Node
	Link      model.LinkKind `json:"link"`
	Direction string         `json:"direction"` // "out" if the entity is the link source
}

// Entity is the response of the entity details endpoint.
type Entity struct {
	Node    model.Node `json:"node"`
	Related []Related  `json:"related"`
	Data    any        `json:"data,omitempty"`
}

func (s *Server) entity(w http.ResponseWriter, r *http.Request) {
	h, ok := s.hub(w, r)
	if !ok {
		return
	}
	reader, inputs, g := h.Snapshot()
	id := r.PathValue("id")
	node, ok := g.Node(id)
	if !ok {
		httpError(w, http.StatusNotFound, "unknown entity")
		return
	}
	e := Entity{Node: node, Related: related(g, id)}
	if reader != nil && inputs != nil {
		data, err := docker.Details(r.Context(), reader, inputs, id)
		switch {
		case errors.Is(err, docker.ErrNotFound):
			httpError(w, http.StatusNotFound, "unknown entity")
			return
		case err != nil:
			httpError(w, http.StatusBadGateway, err.Error())
			return
		}
		e.Data = data
	}
	writeJSON(w, e)
}

func related(g model.Graph, id string) []Related {
	nodes := make(map[string]model.Node, len(g.Nodes))
	for _, n := range g.Nodes {
		nodes[n.ID] = n
	}
	out := []Related{}
	for _, l := range g.Links {
		switch id {
		case l.Source:
			out = append(out, Related{Node: nodes[l.Target], Link: l.Kind, Direction: "out"})
		case l.Target:
			out = append(out, Related{Node: nodes[l.Source], Link: l.Kind, Direction: "in"})
		}
	}
	return out
}

// containerReader resolves the host and checks that the container is part of its graph.
func (s *Server) containerReader(w http.ResponseWriter, r *http.Request) (*docker.Reader, string, bool) {
	h, ok := s.hub(w, r)
	if !ok {
		return nil, "", false
	}
	reader, _, g := h.Snapshot()
	id := r.PathValue("id")
	if _, ok := g.Node(docker.ContainerID(id)); !ok {
		httpError(w, http.StatusNotFound, "unknown container")
		return nil, "", false
	}
	if reader == nil {
		httpError(w, http.StatusServiceUnavailable, "host not connected")
		return nil, "", false
	}
	return reader, id, true
}

func (s *Server) stats(w http.ResponseWriter, r *http.Request) {
	reader, id, ok := s.containerReader(w, r)
	if !ok {
		return
	}
	sse := newSSE(w)
	err := reader.Stats(r.Context(), id, func(st docker.StatsSample) error {
		return sse.send("sample", st)
	})
	if err != nil && r.Context().Err() == nil {
		_ = sse.send("error", map[string]string{"error": err.Error()})
	}
}

func (s *Server) logs(w http.ResponseWriter, r *http.Request) {
	reader, id, ok := s.containerReader(w, r)
	if !ok {
		return
	}
	tail := 200
	if v, err := strconv.Atoi(r.URL.Query().Get("tail")); err == nil && v >= 0 && v <= 5000 {
		tail = v
	}
	follow := r.URL.Query().Get("follow") == "1"
	sse := newSSE(w)
	err := reader.Logs(r.Context(), id, tail, follow, func(l docker.LogLine) error {
		return sse.send("line", l)
	})
	if r.Context().Err() != nil {
		return
	}
	if err != nil {
		_ = sse.send("error", map[string]string{"error": err.Error()})
	}
	_ = sse.send("eof", struct{}{})
}

const notBuiltPage = `<!doctype html><title>dviz</title>
<p>The dviz frontend was not built into this binary. Run <code>npm --prefix web ci &amp;&amp; go generate ./web</code> and rebuild.</p>`

// static serves the embedded single-page app, falling back to index.html for unknown paths.
func (s *Server) static() http.Handler {
	files := http.FileServerFS(s.assets)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			w.Header().Set("Allow", "GET, HEAD")
			httpError(w, http.StatusMethodNotAllowed, "method not allowed")
			return
		}
		if _, err := fs.Stat(s.assets, "index.html"); err != nil {
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(notBuiltPage))
			return
		}
		p := strings.TrimPrefix(path.Clean(r.URL.Path), "/")
		if p == "" {
			p = "index.html"
		}
		if fi, err := fs.Stat(s.assets, p); err != nil || fi.IsDir() {
			r = r.Clone(r.Context())
			r.URL.Path = "/"
		}
		if strings.HasPrefix(p, "assets/") {
			w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
		}
		files.ServeHTTP(w, r)
	})
}
