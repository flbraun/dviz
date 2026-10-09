// Package hub keeps one live graph per Docker host and fans changes out to subscribers.
package hub

import (
	"context"
	"log/slog"
	"sync"
	"time"

	"github.com/flbraun/dviz/internal/config"
	"github.com/flbraun/dviz/internal/docker"
	"github.com/flbraun/dviz/internal/model"
)

// Status is the connection state of a host.
type Status string

const (
	StatusConnecting Status = "connecting"
	StatusConnected  Status = "connected"
	StatusError      Status = "error"
)

// HostInfo is the public description of a configured host.
type HostInfo struct {
	Name          string `json:"name"`
	DisplayName   string `json:"displayName"`
	URL           string `json:"url"`
	Status        Status `json:"status"`
	Error         string `json:"error,omitempty"`
	EngineVersion string `json:"engineVersion,omitempty"`
}

const (
	debounce      = 300 * time.Millisecond
	maxDebounce   = 2 * time.Second
	minBackoff    = time.Second
	maxBackoff    = 30 * time.Second
	callTimeout   = 30 * time.Second
	subscriberBuf = 64
)

// ignoredActions are high-frequency events that never change the graph.
var ignoredActions = map[string]bool{
	"exec_create": true, "exec_start": true, "exec_die": true, "exec_detach": true,
	"attach": true, "detach": true, "resize": true, "top": true,
	"archive-path": true, "extract-to-dir": true, "export": true, "copy": true,
}

// relevantTypes are the event types that can change the graph.
var relevantTypes = map[string]bool{
	"container": true, "network": true, "volume": true, "image": true,
	"service": true, "node": true, "secret": true, "config": true,
}

// Hub watches one Docker host and maintains its graph.
type Hub struct {
	cfg      config.Host
	onChange func()

	mu     sync.RWMutex
	info   HostInfo
	reader *docker.Reader
	inputs *docker.Inputs
	graph  model.Graph
	subs   map[chan model.Diff]struct{}

	cancel context.CancelFunc
	done   chan struct{}
}

func newHub(parent context.Context, cfg config.Host, onChange func()) *Hub {
	ctx, cancel := context.WithCancel(parent)
	h := &Hub{
		cfg:      cfg,
		onChange: onChange,
		info:     HostInfo{Name: cfg.Name, DisplayName: cfg.DisplayName, URL: cfg.Address(), Status: StatusConnecting},
		graph:    statusGraph(StatusConnecting, ""),
		subs:     map[chan model.Diff]struct{}{},
		cancel:   cancel,
		done:     make(chan struct{}),
	}
	go h.run(ctx)
	return h
}

// statusGraph is the graph shown while a host is not connected: just its root node.
func statusGraph(s Status, msg string) model.Graph {
	n := model.Node{ID: docker.HostRootID, Kind: model.KindHost, Status: string(s)}
	if msg != "" {
		n.Attrs = map[string]string{"error": msg}
	}
	return model.Graph{Nodes: []model.Node{n}, Links: []model.Link{}}
}

// Info returns the host's current public description.
func (h *Hub) Info() HostInfo {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return h.info
}

func (h *Hub) setDisplayName(name string) {
	h.mu.Lock()
	h.info.DisplayName = name
	h.cfg.DisplayName = name
	h.mu.Unlock()
}

// Graph returns the current graph.
func (h *Hub) Graph() model.Graph {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return h.graph
}

// Snapshot returns the reader and inputs of the latest successful refresh, or nil while
// the host is not connected.
func (h *Hub) Snapshot() (*docker.Reader, *docker.Inputs, model.Graph) {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return h.reader, h.inputs, h.graph
}

// Subscribe returns the current graph and a channel of subsequent diffs. The channel is
// closed when the subscriber falls too far behind or the hub stops; callers then resubscribe.
func (h *Hub) Subscribe() (model.Graph, <-chan model.Diff, func()) {
	ch := make(chan model.Diff, subscriberBuf)
	h.mu.Lock()
	g := h.graph
	h.subs[ch] = struct{}{}
	h.mu.Unlock()
	return g, ch, func() {
		h.mu.Lock()
		if _, ok := h.subs[ch]; ok {
			delete(h.subs, ch)
			close(ch)
		}
		h.mu.Unlock()
	}
}

// stop terminates the hub and waits for it to finish.
func (h *Hub) stop() {
	h.cancel()
	<-h.done
	h.mu.Lock()
	for ch := range h.subs {
		delete(h.subs, ch)
		close(ch)
	}
	h.mu.Unlock()
}

// setGraph replaces the graph and broadcasts the diff. Must not hold h.mu.
func (h *Hub) setGraph(g model.Graph, r *docker.Reader, in *docker.Inputs) {
	h.mu.Lock()
	d := model.Compute(h.graph, g)
	h.graph, h.reader, h.inputs = g, r, in
	if !d.Empty() {
		for ch := range h.subs {
			select {
			case ch <- d:
			default:
				// Too slow: drop it; the client reconnects and receives a fresh snapshot.
				delete(h.subs, ch)
				close(ch)
			}
		}
	}
	h.mu.Unlock()
}

func (h *Hub) setStatus(s Status, msg, engine string) {
	h.mu.Lock()
	changed := h.info.Status != s || h.info.Error != msg || (engine != "" && h.info.EngineVersion != engine)
	h.info.Status, h.info.Error = s, msg
	if engine != "" {
		h.info.EngineVersion = engine
	}
	h.mu.Unlock()
	if changed && h.onChange != nil {
		h.onChange()
	}
}

func (h *Hub) run(ctx context.Context) {
	defer close(h.done)
	log := slog.With("host", h.cfg.Name)
	backoff := minBackoff
	for {
		started := time.Now()
		err := h.session(ctx)
		if ctx.Err() != nil {
			return
		}
		log.Warn("host disconnected", "err", err, "retry", backoff)
		h.setGraph(statusGraph(StatusError, err.Error()), nil, nil)
		h.setStatus(StatusError, err.Error(), "")
		if time.Since(started) > maxBackoff {
			backoff = minBackoff
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(backoff):
		}
		backoff = min(backoff*2, maxBackoff)
	}
}

// session connects, takes a snapshot and keeps it current until an error occurs.
func (h *Hub) session(ctx context.Context) error {
	r, err := docker.Open(h.cfg)
	if err != nil {
		return err
	}
	defer r.Close()

	vctx, cancel := context.WithTimeout(ctx, callTimeout)
	version, err := r.Version(vctx)
	cancel()
	if err != nil {
		return err
	}

	sctx, cancel := context.WithCancel(ctx)
	defer cancel()
	// Subscribe before the first snapshot so no change between the two is lost.
	events, errc := r.Events(sctx)

	if err := h.refresh(ctx, r); err != nil {
		return err
	}
	h.setStatus(StatusConnected, "", version)
	slog.Info("host connected", "host", h.cfg.Name, "engine", version)
	defer func() {
		h.mu.Lock()
		h.reader = nil
		h.mu.Unlock()
	}()

	timer := time.NewTimer(time.Hour)
	timer.Stop()
	var firstPending time.Time
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case err := <-errc:
			return err
		case ev, ok := <-events:
			if !ok {
				continue // errc delivers the reason
			}
			if !relevantTypes[ev.Type] || ignoredActions[ev.Action] {
				continue
			}
			if firstPending.IsZero() {
				firstPending = time.Now()
			}
			wait := debounce
			if rest := maxDebounce - time.Since(firstPending); rest < wait {
				wait = max(rest, 0)
			}
			timer.Reset(wait)
		case <-timer.C:
			firstPending = time.Time{}
			if err := h.refresh(ctx, r); err != nil {
				return err
			}
		}
	}
}

func (h *Hub) refresh(ctx context.Context, r *docker.Reader) error {
	fctx, cancel := context.WithTimeout(ctx, callTimeout)
	defer cancel()
	in, err := docker.Fetch(fctx, r)
	if err != nil {
		return err
	}
	h.setGraph(docker.Build(in), r, in)
	return nil
}
