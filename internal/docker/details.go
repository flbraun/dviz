package docker

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/moby/moby/api/types/swarm"
)

// ErrNotFound is returned for entities that are not part of the host.
var ErrNotFound = errors.New("not found")

// Ref points at another graph node.
type Ref struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

type ContainerDetails struct {
	ID           string             `json:"id"`
	Name         string             `json:"name"`
	Image        string             `json:"image"`
	ImageID      string             `json:"imageId"`
	Command      []string           `json:"command"`
	Created      string             `json:"created"`
	State        ContainerState     `json:"state"`
	Restart      string             `json:"restartPolicy"`
	RestartCount int                `json:"restartCount"`
	Ports        []PortBinding      `json:"ports"`
	Mounts       []MountInfo        `json:"mounts"`
	Networks     []EndpointInfo     `json:"networks"`
	NetworkMode  string             `json:"networkMode"`
	NetNSOwner   *Ref               `json:"netnsOwner,omitempty"`
	NetNSSharers []Ref              `json:"netnsSharers,omitempty"`
	Peers        []Peer             `json:"peers"`
	Labels       map[string]string  `json:"labels"`
	Resources    ContainerResources `json:"resources"`
	Project      string             `json:"project,omitempty"`
	Service      string             `json:"service,omitempty"`
	Hostname     string             `json:"hostname"`
	User         string             `json:"user,omitempty"`
	WorkingDir   string             `json:"workingDir,omitempty"`
	Tty          bool               `json:"tty"`
}

type ContainerState struct {
	Status        string `json:"status"`
	Running       bool   `json:"running"`
	ExitCode      int    `json:"exitCode"`
	Error         string `json:"error,omitempty"`
	OOMKilled     bool   `json:"oomKilled"`
	StartedAt     string `json:"startedAt"`
	FinishedAt    string `json:"finishedAt"`
	Health        string `json:"health,omitempty"`
	FailingStreak int    `json:"failingStreak,omitempty"`
}

type PortBinding struct {
	Container string `json:"container"`
	HostIP    string `json:"hostIp,omitempty"`
	HostPort  string `json:"hostPort,omitempty"`
}

type MountInfo struct {
	Type        string `json:"type"`
	Name        string `json:"name,omitempty"`
	Source      string `json:"source,omitempty"`
	Destination string `json:"destination"`
	RW          bool   `json:"rw"`
}

type EndpointInfo struct {
	Network  string   `json:"network"`
	NodeID   string   `json:"nodeId"`
	IP       string   `json:"ip,omitempty"`
	IPv6     string   `json:"ipv6,omitempty"`
	Gateway  string   `json:"gateway,omitempty"`
	MAC      string   `json:"mac,omitempty"`
	Aliases  []string `json:"aliases,omitempty"`
	DNSNames []string `json:"dnsNames,omitempty"`
}

type ContainerResources struct {
	Memory    int64  `json:"memory"`
	NanoCPUs  int64  `json:"nanoCpus"`
	PidsLimit *int64 `json:"pidsLimit,omitempty"`
}

type NetworkDetails struct {
	ID         string            `json:"id"`
	Name       string            `json:"name"`
	Driver     string            `json:"driver"`
	Scope      string            `json:"scope"`
	Internal   bool              `json:"internal"`
	Attachable bool              `json:"attachable"`
	Ingress    bool              `json:"ingress"`
	ICC        bool              `json:"icc"`
	DNS        bool              `json:"dns"`
	IPv4       bool              `json:"ipv4"`
	IPv6       bool              `json:"ipv6"`
	Subnets    []Subnet          `json:"subnets"`
	Options    map[string]string `json:"options"`
	Labels     map[string]string `json:"labels"`
	Members    []NetworkMember   `json:"members"`
	ReachGroup bool              `json:"reachGroup"`
}

type Subnet struct {
	Subnet  string `json:"subnet"`
	Gateway string `json:"gateway,omitempty"`
}

type NetworkMember struct {
	ID   string `json:"id"` // graph node ID
	Name string `json:"name"`
	IPv4 string `json:"ipv4,omitempty"`
	IPv6 string `json:"ipv6,omitempty"`
}

type VolumeDetails struct {
	Name       string            `json:"name"`
	Driver     string            `json:"driver"`
	Mountpoint string            `json:"mountpoint"`
	Scope      string            `json:"scope"`
	CreatedAt  string            `json:"createdAt,omitempty"`
	Labels     map[string]string `json:"labels"`
	Options    []string          `json:"options"` // keys only
}

type ImageDetails struct {
	ID           string            `json:"id"`
	Tags         []string          `json:"tags"`
	Digests      []string          `json:"digests"`
	Created      string            `json:"created,omitempty"`
	Size         int64             `json:"size"`
	Arch         string            `json:"arch"`
	OS           string            `json:"os"`
	Author       string            `json:"author,omitempty"`
	Entrypoint   []string          `json:"entrypoint,omitempty"`
	Cmd          []string          `json:"cmd,omitempty"`
	WorkingDir   string            `json:"workingDir,omitempty"`
	User         string            `json:"user,omitempty"`
	ExposedPorts []string          `json:"exposedPorts,omitempty"`
	Labels       map[string]string `json:"labels"`
}

type ServiceDetails struct {
	ID       string               `json:"id"`
	Name     string               `json:"name"`
	Image    string               `json:"image"`
	Mode     string               `json:"mode"`
	Running  uint64               `json:"running"`
	Desired  uint64               `json:"desired"`
	Ports    []string             `json:"ports"`
	Networks []ServiceNetwork     `json:"networks"`
	Secrets  []SwarmFileReference `json:"secrets"`
	Configs  []SwarmFileReference `json:"configs"`
	Labels   map[string]string    `json:"labels"`
	Stack    string               `json:"stack,omitempty"`
	Created  time.Time            `json:"created"`
	Updated  time.Time            `json:"updated"`
	Update   string               `json:"updateState,omitempty"`
}

type ServiceNetwork struct {
	Network string   `json:"network"`
	NodeID  string   `json:"nodeId"`
	Aliases []string `json:"aliases,omitempty"`
}

type SwarmFileReference struct {
	Name   string `json:"name"`
	NodeID string `json:"nodeId"`
	Target string `json:"target,omitempty"`
}

type TaskDetails struct {
	ID          string    `json:"id"`
	Service     string    `json:"service"`
	Slot        int       `json:"slot,omitempty"`
	NodeID      string    `json:"nodeId,omitempty"`
	State       string    `json:"state"`
	Desired     string    `json:"desired"`
	Message     string    `json:"message,omitempty"`
	Err         string    `json:"err,omitempty"`
	ContainerID string    `json:"containerId,omitempty"`
	Image       string    `json:"image,omitempty"`
	Timestamp   time.Time `json:"timestamp"`
	Addresses   []string  `json:"addresses,omitempty"`
}

type SwarmNodeDetails struct {
	ID           string            `json:"id"`
	Hostname     string            `json:"hostname"`
	Role         string            `json:"role"`
	Availability string            `json:"availability"`
	State        string            `json:"state"`
	Addr         string            `json:"addr"`
	Leader       bool              `json:"leader"`
	Reachability string            `json:"reachability,omitempty"`
	Engine       string            `json:"engine"`
	OS           string            `json:"os"`
	Arch         string            `json:"arch"`
	NanoCPUs     int64             `json:"nanoCpus"`
	Memory       int64             `json:"memory"`
	Labels       map[string]string `json:"labels"`
}

type SwarmObjectDetails struct {
	ID      string            `json:"id"`
	Name    string            `json:"name"`
	Labels  map[string]string `json:"labels"`
	Created time.Time         `json:"created"`
	Updated time.Time         `json:"updated"`
	Driver  string            `json:"driver,omitempty"`
}

type HostDetails struct {
	Name          string `json:"name"`
	Engine        string `json:"engine"`
	OS            string `json:"os"`
	Kernel        string `json:"kernel"`
	Arch          string `json:"arch"`
	CPUs          int    `json:"cpus"`
	Memory        int64  `json:"memory"`
	Containers    int    `json:"containers"`
	Running       int    `json:"running"`
	Paused        int    `json:"paused"`
	Stopped       int    `json:"stopped"`
	Images        int    `json:"images"`
	StorageDriver string `json:"storageDriver"`
	CgroupVersion string `json:"cgroupVersion"`
	Swarm         string `json:"swarm"`
}

// Details returns kind-specific data for a graph node ID, using the latest snapshot inputs
// and, where the snapshot lacks data, a sanitized inspect call.
func Details(ctx context.Context, r *Reader, in *Inputs, nodeID string) (any, error) {
	kind, id, ok := strings.Cut(nodeID, "/")
	if !ok && nodeID != HostRootID {
		return nil, ErrNotFound
	}
	switch kind {
	case "host":
		if nodeID == HostRootID {
			return hostDetails(in), nil
		}
		return nil, nil
	case "container":
		return containerDetails(ctx, r, in, id)
	case "network":
		return networkDetails(ctx, r, in, id)
	case "volume":
		for _, v := range in.Volumes {
			if v.Name == id {
				return VolumeDetails{Name: v.Name, Driver: v.Driver, Mountpoint: v.Mountpoint, Scope: v.Scope,
					CreatedAt: v.CreatedAt, Labels: v.Labels, Options: keysOf(v.Options)}, nil
			}
		}
	case "image":
		return imageDetails(ctx, r, id)
	case "service":
		for _, s := range in.Services {
			if s.ID == id {
				return serviceDetails(in, s), nil
			}
		}
	case "task":
		for _, t := range in.Tasks {
			if t.ID == id {
				return taskDetails(in, t), nil
			}
		}
	case "node":
		for _, n := range in.Nodes {
			if n.ID == id {
				return swarmNodeDetails(n), nil
			}
		}
	case "secret":
		for _, s := range in.Secrets {
			if s.ID == id {
				return SwarmObjectDetails{ID: s.ID, Name: s.Spec.Name, Labels: s.Spec.Labels,
					Created: s.CreatedAt, Updated: s.UpdatedAt, Driver: driverName(s.Spec.Driver)}, nil
			}
		}
	case "config":
		for _, c := range in.Configs {
			if c.ID == id {
				return SwarmObjectDetails{ID: c.ID, Name: c.Spec.Name, Labels: c.Spec.Labels,
					Created: c.CreatedAt, Updated: c.UpdatedAt, Driver: driverName(c.Spec.Templating)}, nil
			}
		}
	case "project", "bind", "hostnet":
		// Fully described by the node and its related entities.
		return nil, nil
	}
	return nil, ErrNotFound
}

func hostDetails(in *Inputs) HostDetails {
	i := in.Info
	return HostDetails{Name: i.Name, Engine: i.ServerVersion, OS: i.OperatingSystem, Kernel: i.KernelVersion,
		Arch: i.Architecture, CPUs: i.NCPU, Memory: i.MemTotal, Containers: i.Containers, Running: i.ContainersRunning,
		Paused: i.ContainersPaused, Stopped: i.ContainersStopped, Images: i.Images, StorageDriver: i.Driver,
		CgroupVersion: i.CgroupVersion, Swarm: string(i.Swarm.LocalNodeState)}
}

func containerDetails(ctx context.Context, r *Reader, in *Inputs, id string) (any, error) {
	c, err := r.Container(ctx, id)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrNotFound, err)
	}
	d := ContainerDetails{
		ID: c.ID, Name: strings.TrimPrefix(c.Name, "/"), ImageID: c.Image, Created: c.Created,
		Command: append([]string{c.Path}, c.Args...), RestartCount: c.RestartCount,
		Labels: map[string]string{}, Ports: []PortBinding{}, Mounts: []MountInfo{},
		Networks: []EndpointInfo{}, Peers: in.Peers(c.ID),
	}
	if d.Peers == nil {
		d.Peers = []Peer{}
	}
	resolvePeerNames(ctx, r, d.Peers)
	if s := c.State; s != nil {
		d.State = ContainerState{Status: string(s.Status), Running: s.Running, ExitCode: s.ExitCode, Error: s.Error,
			OOMKilled: s.OOMKilled, StartedAt: s.StartedAt, FinishedAt: s.FinishedAt}
		if s.Health != nil {
			d.State.Health = string(s.Health.Status)
			d.State.FailingStreak = s.Health.FailingStreak
		}
	}
	if cfg := c.Config; cfg != nil {
		d.Image, d.Hostname, d.User, d.WorkingDir, d.Tty = cfg.Image, cfg.Hostname, cfg.User, cfg.WorkingDir, cfg.Tty
		if cfg.Labels != nil {
			d.Labels = cfg.Labels
		}
		d.Project = cfg.Labels[LabelComposeProject]
		d.Service = cfg.Labels[LabelComposeService]
	}
	if hc := c.HostConfig; hc != nil {
		d.Restart = string(hc.RestartPolicy.Name)
		d.NetworkMode = string(hc.NetworkMode)
		d.Resources = ContainerResources{Memory: hc.Memory, NanoCPUs: hc.NanoCPUs, PidsLimit: hc.PidsLimit}
	}
	for _, m := range c.Mounts {
		d.Mounts = append(d.Mounts, MountInfo{Type: string(m.Type), Name: m.Name, Source: m.Source, Destination: m.Destination, RW: m.RW})
	}
	if ns := c.NetworkSettings; ns != nil {
		for port, bindings := range ns.Ports {
			if len(bindings) == 0 {
				d.Ports = append(d.Ports, PortBinding{Container: port.String()})
			}
			for _, b := range bindings {
				pb := PortBinding{Container: port.String(), HostPort: b.HostPort}
				if b.HostIP.IsValid() {
					pb.HostIP = b.HostIP.String()
				}
				d.Ports = append(d.Ports, pb)
			}
		}
		for name, ep := range ns.Networks {
			if ep == nil {
				continue
			}
			e := EndpointInfo{Network: name, NodeID: NetworkID(ep.NetworkID), Aliases: ep.Aliases, DNSNames: ep.DNSNames, MAC: ep.MacAddress.String()}
			if name == "host" {
				e.NodeID = HostNetID
			}
			if ep.IPAddress.IsValid() {
				e.IP = ep.IPAddress.String()
			}
			if ep.GlobalIPv6Address.IsValid() {
				e.IPv6 = ep.GlobalIPv6Address.String()
			}
			if ep.Gateway.IsValid() {
				e.Gateway = ep.Gateway.String()
			}
			d.Networks = append(d.Networks, e)
		}
	}
	sort.Slice(d.Ports, func(i, j int) bool {
		return d.Ports[i].Container+d.Ports[i].HostIP < d.Ports[j].Container+d.Ports[j].HostIP
	})
	sort.Slice(d.Networks, func(i, j int) bool { return d.Networks[i].Network < d.Networks[j].Network })

	x := newReachIndex(in)
	if root, ok := x.root[c.ID]; ok {
		if root != c.ID {
			d.NetNSOwner = &Ref{ID: ContainerID(root), Name: ContainerName(*x.byID[root])}
		}
		for other, r := range x.root {
			if r == c.ID && other != c.ID {
				d.NetNSSharers = append(d.NetNSSharers, Ref{ID: ContainerID(other), Name: ContainerName(*x.byID[other])})
			}
		}
		sort.Slice(d.NetNSSharers, func(i, j int) bool { return d.NetNSSharers[i].Name < d.NetNSSharers[j].Name })
	}
	return d, nil
}

// maxPeerInspects bounds the inspect calls made to resolve peer DNS names.
const maxPeerInspects = 100

// resolvePeerNames replaces the list-derived DNS names of peers with the authoritative
// per-network DNS names from inspecting each peer; the list endpoint omits aliases.
func resolvePeerNames(ctx context.Context, r *Reader, peers []Peer) {
	var wg sync.WaitGroup
	sem := make(chan struct{}, 8)
	for i := range peers {
		if i >= maxPeerInspects {
			break
		}
		wg.Add(1)
		go func(p *Peer) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			c, err := r.Container(ctx, strings.TrimPrefix(p.ID, "container/"))
			if err != nil || c.NetworkSettings == nil {
				return
			}
			for j := range p.Networks {
				pn := &p.Networks[j]
				if ep := c.NetworkSettings.Networks[pn.Network]; ep != nil && pn.DNS && len(ep.DNSNames) > 0 {
					pn.Names = append([]string(nil), ep.DNSNames...)
					sort.Strings(pn.Names)
				}
			}
		}(&peers[i])
	}
	wg.Wait()
}

func networkDetails(ctx context.Context, r *Reader, in *Inputs, id string) (any, error) {
	n, err := r.Network(ctx, id)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrNotFound, err)
	}
	d := NetworkDetails{
		ID: n.ID, Name: n.Name, Driver: n.Driver, Scope: n.Scope, Internal: n.Internal, Attachable: n.Attachable,
		Ingress: n.Ingress, ICC: n.Options[iccOption] != "false", DNS: n.Name != "bridge", IPv4: n.EnableIPv4, IPv6: n.EnableIPv6,
		Options: n.Options, Labels: n.Labels, Subnets: []Subnet{}, Members: []NetworkMember{},
	}
	for _, c := range n.IPAM.Config {
		s := Subnet{}
		if c.Subnet.IsValid() {
			s.Subnet = c.Subnet.String()
		}
		if c.Gateway.IsValid() {
			s.Gateway = c.Gateway.String()
		}
		d.Subnets = append(d.Subnets, s)
	}
	for cid, ep := range n.Containers {
		m := NetworkMember{ID: ContainerID(cid), Name: ep.Name}
		if ep.IPv4Address.IsValid() {
			m.IPv4 = ep.IPv4Address.String()
		}
		if ep.IPv6Address.IsValid() {
			m.IPv6 = ep.IPv6Address.String()
		}
		d.Members = append(d.Members, m)
	}
	sort.Slice(d.Members, func(i, j int) bool { return d.Members[i].Name < d.Members[j].Name })
	d.ReachGroup = newReachIndex(in).grouped[n.ID]
	return d, nil
}

func imageDetails(ctx context.Context, r *Reader, id string) (any, error) {
	img, err := r.Image(ctx, id)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrNotFound, err)
	}
	d := ImageDetails{ID: img.ID, Tags: img.RepoTags, Digests: img.RepoDigests, Created: img.Created, Size: img.Size,
		Arch: img.Architecture, OS: img.Os, Author: img.Author, Labels: map[string]string{}}
	if cfg := img.Config; cfg != nil {
		d.Entrypoint, d.Cmd, d.WorkingDir, d.User = cfg.Entrypoint, cfg.Cmd, cfg.WorkingDir, cfg.User
		if cfg.Labels != nil {
			d.Labels = cfg.Labels
		}
		d.ExposedPorts = keysOf(cfg.ExposedPorts)
	}
	return d, nil
}

func serviceDetails(in *Inputs, s swarm.Service) ServiceDetails {
	d := ServiceDetails{ID: s.ID, Name: s.Spec.Name, Mode: serviceMode(s), Labels: s.Spec.Labels, Stack: s.Spec.Labels[LabelStackNamespace],
		Created: s.CreatedAt, Updated: s.UpdatedAt, Ports: []string{}, Networks: []ServiceNetwork{},
		Secrets: []SwarmFileReference{}, Configs: []SwarmFileReference{}}
	if s.ServiceStatus != nil {
		d.Running, d.Desired = s.ServiceStatus.RunningTasks, s.ServiceStatus.DesiredTasks
	}
	if s.UpdateStatus != nil {
		d.Update = string(s.UpdateStatus.State)
	}
	for _, p := range s.Endpoint.Ports {
		d.Ports = append(d.Ports, fmt.Sprintf("%d->%d/%s (%s)", p.PublishedPort, p.TargetPort, p.Protocol, p.PublishMode))
	}
	netName := map[string]string{}
	for _, n := range in.Networks {
		netName[n.ID] = n.Name
		netName[n.Name] = n.Name
	}
	netID := map[string]string{}
	for _, n := range in.Networks {
		netID[n.ID] = n.ID
		netID[n.Name] = n.ID
	}
	for _, na := range s.Spec.TaskTemplate.Networks {
		d.Networks = append(d.Networks, ServiceNetwork{Network: netName[na.Target], NodeID: NetworkID(netID[na.Target]), Aliases: na.Aliases})
	}
	if cs := s.Spec.TaskTemplate.ContainerSpec; cs != nil {
		d.Image, _, _ = strings.Cut(cs.Image, "@")
		for _, sr := range cs.Secrets {
			if sr == nil {
				continue
			}
			ref := SwarmFileReference{Name: sr.SecretName, NodeID: SecretID(sr.SecretID)}
			if sr.File != nil {
				ref.Target = sr.File.Name
			}
			d.Secrets = append(d.Secrets, ref)
		}
		for _, cr := range cs.Configs {
			if cr == nil {
				continue
			}
			ref := SwarmFileReference{Name: cr.ConfigName, NodeID: ConfigID(cr.ConfigID)}
			if cr.File != nil {
				ref.Target = cr.File.Name
			}
			d.Configs = append(d.Configs, ref)
		}
	}
	return d
}

func taskDetails(in *Inputs, t swarm.Task) TaskDetails {
	d := TaskDetails{ID: t.ID, Slot: t.Slot, NodeID: t.NodeID, State: string(t.Status.State), Desired: string(t.DesiredState),
		Message: t.Status.Message, Err: t.Status.Err, Timestamp: t.Status.Timestamp}
	for _, s := range in.Services {
		if s.ID == t.ServiceID {
			d.Service = s.Spec.Name
		}
	}
	if cs := t.Status.ContainerStatus; cs != nil {
		d.ContainerID = cs.ContainerID
	}
	if cs := t.Spec.ContainerSpec; cs != nil {
		d.Image, _, _ = strings.Cut(cs.Image, "@")
	}
	for _, na := range t.NetworksAttachments {
		for _, a := range na.Addresses {
			d.Addresses = append(d.Addresses, na.Network.Spec.Name+" "+a.String())
		}
	}
	return d
}

func swarmNodeDetails(n swarm.Node) SwarmNodeDetails {
	d := SwarmNodeDetails{ID: n.ID, Hostname: n.Description.Hostname, Role: string(n.Spec.Role), Availability: string(n.Spec.Availability),
		State: string(n.Status.State), Addr: n.Status.Addr, Engine: n.Description.Engine.EngineVersion,
		OS: n.Description.Platform.OS, Arch: n.Description.Platform.Architecture,
		NanoCPUs: n.Description.Resources.NanoCPUs, Memory: n.Description.Resources.MemoryBytes, Labels: n.Spec.Labels}
	if m := n.ManagerStatus; m != nil {
		d.Leader, d.Reachability = m.Leader, string(m.Reachability)
	}
	return d
}

func driverName(d *swarm.Driver) string {
	if d == nil {
		return ""
	}
	return d.Name
}

func keysOf[K comparable, V any](m map[K]V) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, fmt.Sprint(k))
	}
	sort.Strings(out)
	return out
}
