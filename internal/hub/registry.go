package hub

import (
	"context"
	"reflect"
	"sync"

	"github.com/flbraun/dviz/internal/config"
)

// Registry owns the hubs of all configured hosts.
type Registry struct {
	ctx context.Context

	mu    sync.RWMutex
	hubs  map[string]*Hub
	cfgs  map[string]config.Host
	order []string
	subs  map[chan struct{}]struct{}
}

// NewRegistry creates an empty registry; hubs live until ctx is done or Close is called.
func NewRegistry(ctx context.Context) *Registry {
	return &Registry{
		ctx:  ctx,
		hubs: map[string]*Hub{},
		cfgs: map[string]config.Host{},
		subs: map[chan struct{}]struct{}{},
	}
}

// Apply reconciles the running hubs with the configured hosts: new hosts are started,
// removed hosts stopped, hosts with changed connection settings restarted.
func (r *Registry) Apply(hosts []config.Host) {
	r.mu.Lock()
	var stop []*Hub
	want := map[string]bool{}
	order := make([]string, 0, len(hosts))
	for _, h := range hosts {
		want[h.Name] = true
		order = append(order, h.Name)
		old, exists := r.cfgs[h.Name]
		switch {
		case !exists:
			r.hubs[h.Name] = newHub(r.ctx, h, r.notify)
		case old.URL != h.URL || !reflect.DeepEqual(old.TLS, h.TLS):
			stop = append(stop, r.hubs[h.Name])
			r.hubs[h.Name] = newHub(r.ctx, h, r.notify)
		case old.DisplayName != h.DisplayName:
			r.hubs[h.Name].setDisplayName(h.DisplayName)
		}
		r.cfgs[h.Name] = h
	}
	for name, h := range r.hubs {
		if !want[name] {
			stop = append(stop, h)
			delete(r.hubs, name)
			delete(r.cfgs, name)
		}
	}
	r.order = order
	r.mu.Unlock()

	for _, h := range stop {
		h.stop()
	}
	r.notify()
}

// Hosts returns the configured hosts in config order.
func (r *Registry) Hosts() []HostInfo {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]HostInfo, 0, len(r.order))
	for _, name := range r.order {
		out = append(out, r.hubs[name].Info())
	}
	return out
}

// Get returns the hub for a host name.
func (r *Registry) Get(name string) (*Hub, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	h, ok := r.hubs[name]
	return h, ok
}

// SubscribeHosts returns a channel that is signalled whenever the host list or any host's
// status changes. Signals coalesce.
func (r *Registry) SubscribeHosts() (<-chan struct{}, func()) {
	ch := make(chan struct{}, 1)
	r.mu.Lock()
	r.subs[ch] = struct{}{}
	r.mu.Unlock()
	return ch, func() {
		r.mu.Lock()
		delete(r.subs, ch)
		r.mu.Unlock()
	}
}

func (r *Registry) notify() {
	r.mu.RLock()
	defer r.mu.RUnlock()
	for ch := range r.subs {
		select {
		case ch <- struct{}{}:
		default:
		}
	}
}

// Close stops all hubs.
func (r *Registry) Close() {
	r.mu.Lock()
	hubs := r.hubs
	r.hubs, r.cfgs, r.order = map[string]*Hub{}, map[string]config.Host{}, nil
	r.mu.Unlock()
	for _, h := range hubs {
		h.stop()
	}
}
