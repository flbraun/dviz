package docker

import (
	"context"
	"fmt"
	"sort"
	"strconv"
	"strings"

	"github.com/flbraun/dviz/internal/model"
	"github.com/moby/moby/api/types/container"
	"github.com/moby/moby/api/types/image"
	"github.com/moby/moby/api/types/network"
	"github.com/moby/moby/api/types/swarm"
	"github.com/moby/moby/api/types/system"
	"github.com/moby/moby/api/types/volume"
)

// Compose and stack labels used for grouping.
const (
	LabelComposeProject = "com.docker.compose.project"
	LabelComposeService = "com.docker.compose.service"
	LabelStackNamespace = "com.docker.stack.namespace"
)

// HostRootID is the ID of the host root node in every graph.
const HostRootID = "host"

// Inputs is everything a snapshot is built from. All values are already sanitized.
type Inputs struct {
	Info       system.Info
	Containers []container.Summary
	Networks   []network.Summary
	Volumes    []volume.Volume
	Images     []image.Summary
	Services   []swarm.Service
	Tasks      []swarm.Task
	Nodes      []swarm.Node
	Secrets    []swarm.Secret
	Configs    []swarm.Config
}

// Fetch lists every entity kind from the daemon. Swarm entities are only fetched on managers.
func Fetch(ctx context.Context, r *Reader) (*Inputs, error) {
	in := &Inputs{}
	var err error
	if in.Info, err = r.Info(ctx); err != nil {
		return nil, fmt.Errorf("info: %w", err)
	}
	if in.Containers, err = r.Containers(ctx); err != nil {
		return nil, fmt.Errorf("containers: %w", err)
	}
	if in.Networks, err = r.Networks(ctx); err != nil {
		return nil, fmt.Errorf("networks: %w", err)
	}
	if in.Volumes, err = r.Volumes(ctx); err != nil {
		return nil, fmt.Errorf("volumes: %w", err)
	}
	if in.Images, err = r.Images(ctx); err != nil {
		return nil, fmt.Errorf("images: %w", err)
	}
	if !in.Info.Swarm.ControlAvailable {
		return in, nil
	}
	if in.Services, err = r.Services(ctx); err != nil {
		return nil, fmt.Errorf("services: %w", err)
	}
	if in.Tasks, err = r.Tasks(ctx); err != nil {
		return nil, fmt.Errorf("tasks: %w", err)
	}
	if in.Nodes, err = r.Nodes(ctx); err != nil {
		return nil, fmt.Errorf("nodes: %w", err)
	}
	if in.Secrets, err = r.Secrets(ctx); err != nil {
		return nil, fmt.Errorf("secrets: %w", err)
	}
	if in.Configs, err = r.Configs(ctx); err != nil {
		return nil, fmt.Errorf("configs: %w", err)
	}
	return in, nil
}

// Node ID helpers.
func ContainerID(id string) string { return "container/" + id }
func NetworkID(id string) string   { return "network/" + id }
func VolumeID(name string) string  { return "volume/" + name }
func BindID(path string) string    { return "bind/" + path }
func ImageID(id string) string     { return "image/" + id }
func ProjectID(name string) string { return "project/" + name }
func ServiceID(id string) string   { return "service/" + id }
func TaskID(id string) string      { return "task/" + id }
func SwarmNodeID(id string) string { return "node/" + id }
func SecretID(id string) string    { return "secret/" + id }
func ConfigID(id string) string    { return "config/" + id }

// HostNetID is the node representing the host's network stack.
const HostNetID = "hostnet/host"

type builder struct {
	nodes map[string]model.Node
	links map[string]model.Link
}

func (b *builder) node(n model.Node) {
	if _, ok := b.nodes[n.ID]; !ok {
		b.nodes[n.ID] = n
	}
}

func (b *builder) link(l model.Link) {
	if _, ok := b.links[l.ID]; !ok {
		b.links[l.ID] = l
	}
}

// Build turns sanitized inputs into the host's graph. It is a pure function.
func Build(in *Inputs) model.Graph {
	b := &builder{nodes: map[string]model.Node{}, links: map[string]model.Link{}}
	b.node(model.Node{
		ID: HostRootID, Kind: model.KindHost, Name: in.Info.Name, Status: "connected",
		Attrs: map[string]string{
			"engine": in.Info.ServerVersion,
			"os":     in.Info.OperatingSystem,
			"kernel": in.Info.KernelVersion,
			"cpus":   strconv.Itoa(in.Info.NCPU),
			"memory": strconv.FormatInt(in.Info.MemTotal, 10),
			"swarm":  string(in.Info.Swarm.LocalNodeState),
		},
	})

	netByID := map[string]network.Summary{}
	for _, n := range in.Networks {
		netByID[n.ID] = n
	}
	rx := newReachIndex(in)

	b.addNetworks(in, rx)
	b.addVolumes(in)
	b.addImages(in)
	b.addContainers(in, netByID)
	b.addSwarm(in, netByID)
	for _, l := range rx.links() {
		b.link(l)
	}
	for _, l := range exposedLinks(in) {
		b.link(l)
	}

	g := model.Graph{Nodes: make([]model.Node, 0, len(b.nodes)), Links: make([]model.Link, 0, len(b.links))}
	for _, n := range b.nodes {
		g.Nodes = append(g.Nodes, n)
	}
	for _, l := range b.links {
		// Drop links whose endpoints are not part of this snapshot (e.g. a task's container
		// running on another swarm node).
		if _, ok := b.nodes[l.Source]; !ok {
			continue
		}
		if _, ok := b.nodes[l.Target]; !ok {
			continue
		}
		g.Links = append(g.Links, l)
	}
	g.Sort()
	return g
}

func (b *builder) addNetworks(in *Inputs, rx *reachIndex) {
	for _, n := range in.Networks {
		switch n.Name {
		case "none":
			continue
		case "host":
			b.node(model.Node{ID: HostNetID, Kind: model.KindHostNet, Name: "host network"})
			b.link(model.NewLink(HostRootID, HostNetID, model.LinkHost))
			continue
		}
		attrs := map[string]string{"driver": n.Driver, "scope": n.Scope}
		if n.Internal {
			attrs["internal"] = "true"
		}
		if n.Ingress {
			attrs["ingress"] = "true"
		}
		if !iccEnabled(n) {
			attrs["icc"] = "false"
		}
		if n.Name != "bridge" {
			attrs["dns"] = "true"
		}
		if rx.grouped[n.ID] {
			attrs["reachGroup"] = "true"
		}
		b.node(model.Node{ID: NetworkID(n.ID), Kind: model.KindNetwork, Name: n.Name, Attrs: attrs})
		b.link(model.NewLink(HostRootID, NetworkID(n.ID), model.LinkHost))
	}
}

func (b *builder) addVolumes(in *Inputs) {
	for _, v := range in.Volumes {
		b.node(model.Node{ID: VolumeID(v.Name), Kind: model.KindVolume, Name: v.Name,
			Attrs: map[string]string{"driver": v.Driver, "scope": v.Scope}})
		b.link(model.NewLink(HostRootID, VolumeID(v.Name), model.LinkHost))
	}
}

func (b *builder) addImages(in *Inputs) {
	for _, img := range in.Images {
		name, attrs := imageName(img), map[string]string{}
		if len(img.RepoTags) == 0 {
			attrs["dangling"] = "true"
		}
		attrs["size"] = strconv.FormatInt(img.Size, 10)
		b.node(model.Node{ID: ImageID(img.ID), Kind: model.KindImage, Name: name, Attrs: attrs})
		b.link(model.NewLink(HostRootID, ImageID(img.ID), model.LinkHost))
	}
}

func imageName(img image.Summary) string {
	for _, t := range img.RepoTags {
		if t != "<none>:<none>" {
			return t
		}
	}
	for _, d := range img.RepoDigests {
		repo, _, _ := strings.Cut(d, "@")
		return repo + "@" + shortID(img.ID)
	}
	return shortID(img.ID)
}

func shortID(id string) string {
	_, id, _ = strings.Cut(id, ":")
	if len(id) > 12 {
		return id[:12]
	}
	return id
}

// ContainerName returns the container's primary name without the leading slash.
func ContainerName(c container.Summary) string {
	if len(c.Names) > 0 {
		return strings.TrimPrefix(c.Names[0], "/")
	}
	return shortID(c.ID)
}

func (b *builder) addContainers(in *Inputs, netByID map[string]network.Summary) {
	for _, c := range in.Containers {
		id := ContainerID(c.ID)
		project := c.Labels[LabelComposeProject]
		if project == "" {
			project = c.Labels[LabelStackNamespace]
		}
		attrs := map[string]string{"image": c.Image, "statusText": c.Status}
		if c.Health != nil && c.Health.Status != "" && c.Health.Status != container.NoHealthcheck {
			attrs["health"] = string(c.Health.Status)
		}
		if s := c.Labels[LabelComposeService]; s != "" {
			attrs["service"] = s
		}
		mode := container.NetworkMode(c.HostConfig.NetworkMode)
		attrs["networkMode"] = string(mode)
		if mode.IsNone() {
			attrs["isolated"] = "true"
		}
		b.node(model.Node{ID: id, Kind: model.KindContainer, Name: ContainerName(c), Status: string(c.State), Group: project, Attrs: attrs})

		if project != "" {
			b.node(model.Node{ID: ProjectID(project), Kind: model.KindProject, Name: project})
			b.link(model.NewLink(HostRootID, ProjectID(project), model.LinkHost))
			b.link(model.NewLink(id, ProjectID(project), model.LinkProject))
		} else {
			b.link(model.NewLink(HostRootID, id, model.LinkHost))
		}

		if c.ImageID != "" {
			b.link(model.NewLink(id, ImageID(c.ImageID), model.LinkImage))
		}

		if c.NetworkSettings != nil {
			for name, ep := range c.NetworkSettings.Networks {
				if ep == nil {
					continue
				}
				switch name {
				case "none":
				case "host":
					b.link(model.NewLink(id, HostNetID, model.LinkHostNet))
				default:
					netID := ep.NetworkID
					if _, ok := netByID[netID]; ok {
						b.link(model.NewLink(id, NetworkID(netID), model.LinkNetwork))
					}
				}
			}
		}

		for _, m := range c.Mounts {
			switch m.Type {
			case "volume":
				if m.Name != "" {
					l := model.NewLink(id, VolumeID(m.Name), model.LinkMount)
					b.link(l)
				}
			case "bind":
				b.node(model.Node{ID: BindID(m.Source), Kind: model.KindBind, Name: m.Source})
				b.link(model.NewLink(id, BindID(m.Source), model.LinkMount))
			}
		}
	}
}

func (b *builder) addSwarm(in *Inputs, netByID map[string]network.Summary) {
	if len(in.Services) == 0 && len(in.Nodes) == 0 {
		return
	}
	netByName := map[string]string{}
	for _, n := range in.Networks {
		netByName[n.Name] = n.ID
	}
	for _, n := range in.Nodes {
		attrs := map[string]string{"role": string(n.Spec.Role), "availability": string(n.Spec.Availability), "addr": n.Status.Addr}
		if n.ManagerStatus != nil && n.ManagerStatus.Leader {
			attrs["leader"] = "true"
		}
		b.node(model.Node{ID: SwarmNodeID(n.ID), Kind: model.KindNode, Name: n.Description.Hostname, Status: string(n.Status.State), Attrs: attrs})
		b.link(model.NewLink(HostRootID, SwarmNodeID(n.ID), model.LinkHost))
	}

	usedSecrets, usedConfigs := map[string]bool{}, map[string]bool{}
	serviceName := map[string]string{}
	for _, s := range in.Services {
		id := ServiceID(s.ID)
		serviceName[s.ID] = s.Spec.Name
		stack := s.Spec.Labels[LabelStackNamespace]
		attrs := map[string]string{"mode": serviceMode(s)}
		status := ""
		if s.ServiceStatus != nil {
			status = fmt.Sprintf("%d/%d", s.ServiceStatus.RunningTasks, s.ServiceStatus.DesiredTasks)
			attrs["running"] = strconv.FormatUint(s.ServiceStatus.RunningTasks, 10)
			attrs["desired"] = strconv.FormatUint(s.ServiceStatus.DesiredTasks, 10)
		}
		cs := s.Spec.TaskTemplate.ContainerSpec
		if cs != nil {
			img, _, _ := strings.Cut(cs.Image, "@")
			attrs["image"] = img
		}
		b.node(model.Node{ID: id, Kind: model.KindService, Name: s.Spec.Name, Status: status, Group: stack, Attrs: attrs})
		if stack != "" {
			b.node(model.Node{ID: ProjectID(stack), Kind: model.KindProject, Name: stack})
			b.link(model.NewLink(HostRootID, ProjectID(stack), model.LinkHost))
			b.link(model.NewLink(id, ProjectID(stack), model.LinkProject))
		} else {
			b.link(model.NewLink(HostRootID, id, model.LinkHost))
		}
		for _, na := range s.Spec.TaskTemplate.Networks {
			netID := na.Target
			if nid, ok := netByName[netID]; ok {
				netID = nid
			}
			if _, ok := netByID[netID]; ok {
				b.link(model.NewLink(id, NetworkID(netID), model.LinkNetwork))
			}
		}
		if cs != nil {
			for _, sr := range cs.Secrets {
				if sr != nil {
					usedSecrets[sr.SecretID] = true
					b.link(model.NewLink(id, SecretID(sr.SecretID), model.LinkSecret))
				}
			}
			for _, cr := range cs.Configs {
				if cr != nil {
					usedConfigs[cr.ConfigID] = true
					b.link(model.NewLink(id, ConfigID(cr.ConfigID), model.LinkConfig))
				}
			}
		}
	}

	for _, t := range in.Tasks {
		if t.DesiredState == swarm.TaskStateShutdown || t.DesiredState == swarm.TaskStateRemove {
			continue
		}
		id := TaskID(t.ID)
		name := serviceName[t.ServiceID]
		if t.Slot > 0 {
			name += "." + strconv.Itoa(t.Slot)
		} else {
			name += "." + shortID(t.NodeID)
		}
		attrs := map[string]string{"desired": string(t.DesiredState)}
		if t.Status.Err != "" {
			attrs["error"] = t.Status.Err
		}
		b.node(model.Node{ID: id, Kind: model.KindTask, Name: name, Status: string(t.Status.State), Attrs: attrs})
		b.link(model.NewLink(ServiceID(t.ServiceID), id, model.LinkTask))
		if t.NodeID != "" {
			b.link(model.NewLink(id, SwarmNodeID(t.NodeID), model.LinkPlaced))
		}
		if cs := t.Status.ContainerStatus; cs != nil && cs.ContainerID != "" {
			b.link(model.NewLink(id, ContainerID(cs.ContainerID), model.LinkRuns))
		}
	}

	for _, s := range in.Secrets {
		b.node(model.Node{ID: SecretID(s.ID), Kind: model.KindSecret, Name: s.Spec.Name})
		if !usedSecrets[s.ID] {
			b.link(model.NewLink(HostRootID, SecretID(s.ID), model.LinkHost))
		}
	}
	for _, c := range in.Configs {
		b.node(model.Node{ID: ConfigID(c.ID), Kind: model.KindConfig, Name: c.Spec.Name})
		if !usedConfigs[c.ID] {
			b.link(model.NewLink(HostRootID, ConfigID(c.ID), model.LinkHost))
		}
	}
}

func serviceMode(s swarm.Service) string {
	m := s.Spec.Mode
	switch {
	case m.Global != nil:
		return "global"
	case m.ReplicatedJob != nil:
		return "replicated-job"
	case m.GlobalJob != nil:
		return "global-job"
	default:
		return "replicated"
	}
}

// exposedLinks connects containers and services that publish ports to the host root.
func exposedLinks(in *Inputs) []model.Link {
	var out []model.Link
	for _, c := range in.Containers {
		set := map[string]bool{}
		for _, p := range c.Ports {
			if p.PublicPort == 0 {
				continue
			}
			set[fmt.Sprintf("%d->%d/%s", p.PublicPort, p.PrivatePort, p.Type)] = true
		}
		if len(set) > 0 {
			l := model.NewLink(ContainerID(c.ID), HostRootID, model.LinkExposed)
			l.Ports = sortedKeys(set)
			out = append(out, l)
		}
	}
	for _, s := range in.Services {
		set := map[string]bool{}
		for _, p := range s.Endpoint.Ports {
			if p.PublishedPort == 0 {
				continue
			}
			set[fmt.Sprintf("%d->%d/%s", p.PublishedPort, p.TargetPort, p.Protocol)] = true
		}
		if len(set) > 0 {
			l := model.NewLink(ServiceID(s.ID), HostRootID, model.LinkExposed)
			l.Ports = sortedKeys(set)
			out = append(out, l)
		}
	}
	return out
}

func sortedKeys(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
