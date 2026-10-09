// Package model defines the per-host entity graph streamed to the frontend.
package model

import (
	"reflect"
	"sort"
)

// Kind is the entity type of a node.
type Kind string

const (
	KindHost      Kind = "host"
	KindContainer Kind = "container"
	KindNetwork   Kind = "network"
	KindHostNet   Kind = "hostnet"
	KindVolume    Kind = "volume"
	KindBind      Kind = "bind"
	KindImage     Kind = "image"
	KindProject   Kind = "project"
	KindService   Kind = "service"
	KindTask      Kind = "task"
	KindNode      Kind = "node"
	KindSecret    Kind = "secret"
	KindConfig    Kind = "config"
)

// LinkKind is the relationship type of a link.
type LinkKind string

const (
	// Structural (topology) links.
	LinkHost    LinkKind = "host"    // host root -> top-level entity
	LinkNetwork LinkKind = "network" // container/service -> network it joins
	LinkMount   LinkKind = "mount"   // container -> volume/bind
	LinkImage   LinkKind = "image"   // container -> image
	LinkProject LinkKind = "project" // container/service -> compose project / stack
	LinkTask    LinkKind = "task"    // service -> task
	LinkRuns    LinkKind = "runs"    // task -> container
	LinkPlaced  LinkKind = "placed"  // task -> swarm node
	LinkSecret  LinkKind = "secret"  // service -> secret
	LinkConfig  LinkKind = "config"  // service -> config

	// Reachability links.
	LinkReach   LinkKind = "reach"   // container<->container or service<->service sharing a network
	LinkNetNS   LinkKind = "netns"   // container -> container whose network namespace it shares
	LinkHostNet LinkKind = "hostnet" // container -> host network stack
	LinkExposed LinkKind = "exposed" // container/service -> host root, publishes ports
)

// Node is one entity in a host's graph. IDs are "<kind>/<id>", unique per host.
type Node struct {
	ID     string            `json:"id"`
	Kind   Kind              `json:"kind"`
	Name   string            `json:"name"`
	Status string            `json:"status,omitempty"`
	Group  string            `json:"group,omitempty"`
	Attrs  map[string]string `json:"attrs,omitempty"`
}

// Link is a relationship between two nodes. Reach links are undirected; Source < Target.
type Link struct {
	ID     string   `json:"id"`
	Source string   `json:"source"`
	Target string   `json:"target"`
	Kind   LinkKind `json:"kind"`
	// Networks lists the shared network names (reach).
	Networks []string `json:"networks,omitempty"`
	// DNS reports whether peers can resolve each other by name (reach).
	DNS bool `json:"dns,omitempty"`
	// Ports lists published ports (exposed).
	Ports []string `json:"ports,omitempty"`
}

// NewLink builds a link with its canonical ID.
func NewLink(source, target string, kind LinkKind) Link {
	return Link{ID: source + "|" + string(kind) + "|" + target, Source: source, Target: target, Kind: kind}
}

// Graph is a full snapshot of one host.
type Graph struct {
	Nodes []Node `json:"nodes"`
	Links []Link `json:"links"`
}

// Sort orders nodes and links by ID so snapshots are deterministic.
func (g *Graph) Sort() {
	sort.Slice(g.Nodes, func(i, j int) bool { return g.Nodes[i].ID < g.Nodes[j].ID })
	sort.Slice(g.Links, func(i, j int) bool { return g.Links[i].ID < g.Links[j].ID })
}

// Node returns the node with the given ID.
func (g *Graph) Node(id string) (Node, bool) {
	for _, n := range g.Nodes {
		if n.ID == id {
			return n, true
		}
	}
	return Node{}, false
}

// Diff is the change between two graphs of the same host.
type Diff struct {
	AddNodes    []Node   `json:"addNodes,omitempty"`
	UpdateNodes []Node   `json:"updateNodes,omitempty"`
	RemoveNodes []string `json:"removeNodes,omitempty"`
	AddLinks    []Link   `json:"addLinks,omitempty"`
	UpdateLinks []Link   `json:"updateLinks,omitempty"`
	RemoveLinks []string `json:"removeLinks,omitempty"`
}

// Empty reports whether the diff contains no changes.
func (d Diff) Empty() bool {
	return len(d.AddNodes)+len(d.UpdateNodes)+len(d.RemoveNodes)+
		len(d.AddLinks)+len(d.UpdateLinks)+len(d.RemoveLinks) == 0
}

// Compute returns the changes that turn prev into next.
func Compute(prev, next Graph) Diff {
	var d Diff
	d.AddNodes, d.UpdateNodes, d.RemoveNodes = diffByID(prev.Nodes, next.Nodes, func(n Node) string { return n.ID })
	d.AddLinks, d.UpdateLinks, d.RemoveLinks = diffByID(prev.Links, next.Links, func(l Link) string { return l.ID })
	return d
}

func diffByID[T any](prev, next []T, id func(T) string) (added, updated []T, removed []string) {
	old := make(map[string]T, len(prev))
	for _, v := range prev {
		old[id(v)] = v
	}
	for _, v := range next {
		k := id(v)
		o, ok := old[k]
		switch {
		case !ok:
			added = append(added, v)
		case !reflect.DeepEqual(o, v):
			updated = append(updated, v)
		}
		delete(old, k)
	}
	for k := range old {
		removed = append(removed, k)
	}
	sort.Strings(removed)
	return added, updated, removed
}
