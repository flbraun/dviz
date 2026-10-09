package docker

import (
	"sort"
	"strings"

	"github.com/flbraun/dviz/internal/model"
	"github.com/moby/moby/api/types/container"
	"github.com/moby/moby/api/types/network"
)

// MaxReachGroup is the member count above which a network no longer emits pairwise reach
// links; it is marked as a reach group instead.
const MaxReachGroup = 50

const iccOption = "com.docker.network.bridge.enable_icc"

func iccEnabled(n network.Summary) bool {
	return n.Options[iccOption] != "false"
}

// reachIndex answers "which containers can talk to each other" for one snapshot.
//
// Rules: containers sharing a network can reach each other, unless the network disables
// inter-container communication (enable_icc=false) or is the swarm ingress network.
// Containers in another container's network namespace (network_mode: container:x) share
// x's endpoints and therefore x's peers. host and none networks never produce peers.
type reachIndex struct {
	byID    map[string]*container.Summary
	root    map[string]string   // container ID -> ID of the container owning its network namespace
	members map[string][]string // network ID -> container IDs (including namespace sharers)
	nets    map[string]network.Summary
	grouped map[string]bool // networks with too many members for pairwise links
	in      *Inputs
}

func newReachIndex(in *Inputs) *reachIndex {
	x := &reachIndex{
		byID:    map[string]*container.Summary{},
		root:    map[string]string{},
		members: map[string][]string{},
		nets:    map[string]network.Summary{},
		grouped: map[string]bool{},
		in:      in,
	}
	byName := map[string]string{}
	for i := range in.Containers {
		c := &in.Containers[i]
		x.byID[c.ID] = c
		for _, n := range c.Names {
			byName[strings.TrimPrefix(n, "/")] = c.ID
		}
	}
	for _, n := range in.Networks {
		x.nets[n.ID] = n
	}

	resolve := func(ref string) string {
		if _, ok := x.byID[ref]; ok {
			return ref
		}
		if id, ok := byName[ref]; ok {
			return id
		}
		for id := range x.byID {
			if len(ref) >= 12 && strings.HasPrefix(id, ref) {
				return id
			}
		}
		return ""
	}
	for id := range x.byID {
		cur, seen := id, map[string]bool{}
		for !seen[cur] {
			seen[cur] = true
			mode := container.NetworkMode(x.byID[cur].HostConfig.NetworkMode)
			if !mode.IsContainer() {
				break
			}
			next := resolve(mode.ConnectedContainer())
			if next == "" {
				break
			}
			cur = next
		}
		x.root[id] = cur
	}

	for id := range x.byID {
		for _, ep := range x.endpoints(id) {
			x.members[ep.NetworkID] = append(x.members[ep.NetworkID], id)
		}
	}
	for netID, m := range x.members {
		sort.Strings(m)
		if len(m) > MaxReachGroup {
			x.grouped[netID] = true
		}
	}
	return x
}

// endpoints returns the network endpoints a container effectively has (its namespace root's).
func (x *reachIndex) endpoints(id string) map[string]*network.EndpointSettings {
	c := x.byID[x.root[id]]
	if c == nil || c.NetworkSettings == nil {
		return nil
	}
	out := map[string]*network.EndpointSettings{}
	for name, ep := range c.NetworkSettings.Networks {
		if ep != nil && x.eligible(ep.NetworkID) {
			out[name] = ep
		}
	}
	return out
}

// eligible reports whether membership in the network lets containers reach each other.
func (x *reachIndex) eligible(netID string) bool {
	n, ok := x.nets[netID]
	if !ok {
		return false
	}
	if n.Name == "host" || n.Name == "none" || n.Ingress {
		return false
	}
	return iccEnabled(n)
}

func (x *reachIndex) links() []model.Link {
	type acc struct {
		nets map[string]bool
		dns  bool
	}
	pairs := map[[2]string]*acc{}
	for netID, members := range x.members {
		if x.grouped[netID] {
			continue
		}
		n := x.nets[netID]
		for i := 0; i < len(members); i++ {
			for j := i + 1; j < len(members); j++ {
				a, b := members[i], members[j]
				if x.root[a] == x.root[b] {
					continue // same namespace: linked via netns instead
				}
				k := [2]string{ContainerID(a), ContainerID(b)}
				p := pairs[k]
				if p == nil {
					p = &acc{nets: map[string]bool{}}
					pairs[k] = p
				}
				p.nets[n.Name] = true
				p.dns = p.dns || n.Name != "bridge"
			}
		}
	}

	var out []model.Link
	for k, p := range pairs {
		l := model.NewLink(k[0], k[1], model.LinkReach)
		l.Networks = sortedKeys(p.nets)
		l.DNS = p.dns
		out = append(out, l)
	}
	for id, root := range x.root {
		if id != root {
			out = append(out, model.NewLink(ContainerID(id), ContainerID(root), model.LinkNetNS))
		}
	}
	out = append(out, x.serviceLinks()...)
	return out
}

// serviceLinks connects swarm services that share an eligible network.
func (x *reachIndex) serviceLinks() []model.Link {
	byName := map[string]string{}
	for _, n := range x.in.Networks {
		byName[n.Name] = n.ID
	}
	members := map[string][]string{}
	for _, s := range x.in.Services {
		for _, na := range s.Spec.TaskTemplate.Networks {
			netID := na.Target
			if id, ok := byName[netID]; ok {
				netID = id
			}
			if x.eligible(netID) {
				members[netID] = append(members[netID], s.ID)
			}
		}
	}
	pairs := map[[2]string]map[string]bool{}
	for netID, m := range members {
		sort.Strings(m)
		for i := 0; i < len(m); i++ {
			for j := i + 1; j < len(m); j++ {
				k := [2]string{ServiceID(m[i]), ServiceID(m[j])}
				if pairs[k] == nil {
					pairs[k] = map[string]bool{}
				}
				pairs[k][x.nets[netID].Name] = true
			}
		}
	}
	var out []model.Link
	for k, nets := range pairs {
		l := model.NewLink(k[0], k[1], model.LinkReach)
		l.Networks = sortedKeys(nets)
		l.DNS = true
		out = append(out, l)
	}
	return out
}

// Peer is a container reachable from another container.
type Peer struct {
	ID       string        `json:"id"` // graph node ID
	Name     string        `json:"name"`
	State    string        `json:"state"`
	Networks []PeerNetwork `json:"networks"`
}

// PeerNetwork describes how a peer is reachable on one shared network.
type PeerNetwork struct {
	Network string   `json:"network"`
	DNS     bool     `json:"dns"`
	Names   []string `json:"names,omitempty"` // DNS names resolvable on this network
	IP      string   `json:"ip,omitempty"`
}

// Peers lists every container reachable from the given container (Docker ID), including
// members of large "reach group" networks.
func (in *Inputs) Peers(id string) []Peer {
	x := newReachIndex(in)
	if _, ok := x.byID[id]; !ok {
		return nil
	}
	mine := x.endpoints(id)
	var out []Peer
	for otherID, other := range x.byID {
		if x.root[otherID] == x.root[id] {
			continue
		}
		theirs := x.endpoints(otherID)
		var nets []PeerNetwork
		for name, ep := range theirs {
			if _, shared := mine[name]; !shared {
				continue
			}
			pn := PeerNetwork{Network: name, DNS: name != "bridge"}
			if pn.DNS {
				pn.Names = dnsNames(ep, other)
			}
			if ep.IPAddress.IsValid() {
				pn.IP = ep.IPAddress.String()
			}
			nets = append(nets, pn)
		}
		if len(nets) == 0 {
			continue
		}
		sort.Slice(nets, func(i, j int) bool { return nets[i].Network < nets[j].Network })
		out = append(out, Peer{ID: ContainerID(otherID), Name: ContainerName(*other), State: string(other.State), Networks: nets})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

func dnsNames(ep *network.EndpointSettings, c *container.Summary) []string {
	set := map[string]bool{}
	for _, n := range ep.DNSNames {
		set[n] = true
	}
	if len(set) == 0 {
		// The list endpoint omits DNS names; approximate them. Details resolve the real
		// names by inspecting the peer.
		set[ContainerName(*c)] = true
		for _, a := range ep.Aliases {
			set[a] = true
		}
		if s := c.Labels[LabelComposeService]; s != "" {
			set[s] = true
		}
	}
	return sortedKeys(set)
}
