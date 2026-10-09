package integration

import (
	"context"
	"encoding/json"
	"net/netip"
	"slices"
	"testing"
	"time"

	"github.com/flbraun/dviz/internal/docker"
	"github.com/flbraun/dviz/internal/model"
	"github.com/moby/moby/api/types/container"
	"github.com/moby/moby/api/types/mount"
	"github.com/moby/moby/api/types/network"
	"github.com/moby/moby/client"
)

func mustAddr(s string) netip.Addr { return netip.MustParseAddr(s) }

func TestGraphStructure(t *testing.T) {
	c := daemon(t)
	ctx := context.Background()
	project := name(t, "proj")
	netID := net(t, c, name(t, "graph-net"), nil)
	vol, err := c.VolumeCreate(ctx, client.VolumeCreateOptions{Name: name(t, "graph-vol"), Labels: labels(nil)})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, _ = c.VolumeRemove(context.Background(), vol.Volume.Name, client.VolumeRemoveOptions{Force: true})
	})
	bindDir := t.TempDir()

	compose := func(svc string) map[string]string {
		return map[string]string{docker.LabelComposeProject: project, docker.LabelComposeService: svc}
	}
	webID := run(t, c, ctr{
		Name: name(t, "web"), Labels: compose("web"),
		Networks: map[string]*network.EndpointSettings{name(t, "graph-net"): {}},
		Host: &container.HostConfig{Mounts: []mount.Mount{
			{Type: mount.TypeVolume, Source: vol.Volume.Name, Target: "/data"},
			{Type: mount.TypeBind, Source: bindDir, Target: "/host", ReadOnly: true},
		}},
	})
	workerID := run(t, c, ctr{Name: name(t, "worker"), Labels: compose("worker"), NoStart: true})

	in := start(t, localYAML())
	in.waitConnected("local")
	g := in.waitGraph("local", func(g model.Graph) string {
		if !hasNode(g, docker.ContainerID(webID)) || !hasNode(g, docker.ContainerID(workerID)) {
			return "fixture containers missing"
		}
		return ""
	})

	web := docker.ContainerID(webID)
	for _, want := range []struct{ src, kind, dst string }{
		{web, "network", docker.NetworkID(netID)},
		{web, "mount", docker.VolumeID(vol.Volume.Name)},
		{web, "mount", docker.BindID(bindDir)},
		{web, "project", docker.ProjectID(project)},
		{docker.ContainerID(workerID), "project", docker.ProjectID(project)},
		{docker.HostRootID, "host", docker.ProjectID(project)},
		{docker.HostRootID, "host", docker.NetworkID(netID)},
	} {
		if findLink(g, want.src, want.kind, want.dst) == nil {
			t.Errorf("missing %s link %s -> %s", want.kind, want.src, want.dst)
		}
	}
	webNode, _ := g.Node(web)
	if webNode.Status != "running" || webNode.Group != project {
		t.Errorf("web node = %+v", webNode)
	}
	worker, _ := g.Node(docker.ContainerID(workerID))
	if worker.Status != "created" {
		t.Errorf("worker status = %q, want created", worker.Status)
	}
	img := false
	for _, l := range g.Links {
		if l.Source == web && l.Kind == model.LinkImage {
			img = hasNode(g, l.Target)
		}
	}
	if !img {
		t.Error("web has no link to an image node")
	}

	// Details expose the curated view plus related entities.
	var e struct {
		Node    model.Node `json:"node"`
		Related []struct {
			ID   string `json:"id"`
			Link string `json:"link"`
		} `json:"related"`
		Data docker.ContainerDetails `json:"data"`
	}
	in.get(entityPath("local", web), &e)
	if e.Data.Project != project || e.Data.Service != "web" || len(e.Data.Mounts) != 2 {
		t.Errorf("details = %+v", e.Data)
	}
	if !slices.ContainsFunc(e.Related, func(r struct {
		ID   string `json:"id"`
		Link string `json:"link"`
	}) bool {
		return r.ID == docker.VolumeID(vol.Volume.Name) && r.Link == "mount"
	}) {
		t.Errorf("related lacks the volume: %+v", e.Related)
	}
}

func TestLiveUpdates(t *testing.T) {
	c := daemon(t)
	ctx := context.Background()
	in := start(t, localYAML())
	in.waitConnected("local")
	events := in.stream("/api/hosts/local/graph/events")
	next(t, events, 10*time.Second, func(e sseEvent) bool { return e.Event == "snapshot" })

	diffWith := func(pred func(model.Diff) bool) {
		t.Helper()
		next(t, events, 15*time.Second, func(e sseEvent) bool {
			if e.Event != "diff" {
				return false
			}
			var d model.Diff
			if err := json.Unmarshal([]byte(e.Data), &d); err != nil {
				t.Fatal(err)
			}
			return pred(d)
		})
	}

	id := run(t, c, ctr{Name: name(t, "live")})
	nodeID := docker.ContainerID(id)
	diffWith(func(d model.Diff) bool {
		return slices.ContainsFunc(d.AddNodes, func(n model.Node) bool { return n.ID == nodeID && n.Status == "running" })
	})

	timeout := 0
	if _, err := c.ContainerStop(ctx, id, client.ContainerStopOptions{Timeout: &timeout}); err != nil {
		t.Fatal(err)
	}
	diffWith(func(d model.Diff) bool {
		return slices.ContainsFunc(d.UpdateNodes, func(n model.Node) bool { return n.ID == nodeID && n.Status == "exited" })
	})

	if _, err := c.ContainerRemove(ctx, id, client.ContainerRemoveOptions{Force: true}); err != nil {
		t.Fatal(err)
	}
	diffWith(func(d model.Diff) bool { return slices.Contains(d.RemoveNodes, nodeID) })
}

func TestReachability(t *testing.T) {
	c := daemon(t)
	ctx := context.Background()
	n1, n2, iso := name(t, "n1"), name(t, "n2"), name(t, "noicc")
	net(t, c, n1, nil)
	net(t, c, n2, nil)
	net(t, c, iso, map[string]string{"com.docker.network.bridge.enable_icc": "false"})

	on := func(n string, aliases ...string) map[string]*network.EndpointSettings {
		return map[string]*network.EndpointSettings{n: {Aliases: aliases}}
	}
	a := run(t, c, ctr{Name: name(t, "a"), Networks: on(n1)})
	b := run(t, c, ctr{Name: name(t, "b"), Networks: on(n1, "b-alias")})
	cc := run(t, c, ctr{Name: name(t, "c"), Networks: on(n2)})
	d := run(t, c, ctr{Name: name(t, "d"), Networks: on(iso)})
	e := run(t, c, ctr{Name: name(t, "e"), Networks: on(iso)})
	f := run(t, c, ctr{Name: name(t, "f"), Host: &container.HostConfig{NetworkMode: container.NetworkMode("container:" + a)}})
	g := run(t, c, ctr{Name: name(t, "g"), Host: &container.HostConfig{NetworkMode: "none"}})
	port := network.MustParsePort("80/tcp")
	h := run(t, c, ctr{Name: name(t, "h"), Host: &container.HostConfig{PortBindings: network.PortMap{port: {{HostIP: mustAddr("127.0.0.1")}}}}})
	_ = h

	A, B, C, D, E, F, G, H := docker.ContainerID(a), docker.ContainerID(b), docker.ContainerID(cc), docker.ContainerID(d),
		docker.ContainerID(e), docker.ContainerID(f), docker.ContainerID(g), docker.ContainerID(h)

	in := start(t, localYAML())
	in.waitConnected("local")
	gr := in.waitGraph("local", func(g model.Graph) string {
		for _, id := range []string{A, B, C, D, E, F, G, H} {
			if !hasNode(g, id) {
				return "missing " + id
			}
		}
		return ""
	})

	ab := findLink(gr, A, "reach", B)
	if ab == nil || !ab.DNS || !slices.Equal(ab.Networks, []string{n1}) {
		t.Errorf("A<->B reach = %+v", ab)
	}
	if findLink(gr, A, "reach", C) != nil {
		t.Error("A and C share no network but are linked")
	}
	if findLink(gr, D, "reach", E) != nil {
		t.Error("D and E are on an ICC-disabled network but are linked")
	}
	if findLink(gr, F, "netns", A) == nil {
		t.Error("F has no netns link to A")
	}
	if findLink(gr, F, "reach", B) == nil {
		t.Error("F does not inherit A's peer B")
	}
	if findLink(gr, F, "reach", A) != nil {
		t.Error("F and A share a namespace and must not have a reach link")
	}
	if n, _ := gr.Node(G); n.Attrs["isolated"] != "true" {
		t.Errorf("G attrs = %v, want isolated", n.Attrs)
	}
	if l := findLink(gr, H, "exposed", docker.HostRootID); l == nil || len(l.Ports) != 1 {
		t.Errorf("H exposed link = %+v", l)
	}

	// Details list peers with their DNS names, including aliases.
	var det struct {
		Data docker.ContainerDetails `json:"data"`
	}
	in.get(entityPath("local", A), &det)
	var peerB *docker.Peer
	for i, p := range det.Data.Peers {
		if p.ID == C {
			t.Error("A's peers include C")
		}
		if p.ID == B {
			peerB = &det.Data.Peers[i]
		}
	}
	if peerB == nil || len(peerB.Networks) != 1 || !slices.Contains(peerB.Networks[0].Names, "b-alias") || !slices.Contains(peerB.Networks[0].Names, name(t, "b")) {
		t.Errorf("peer B = %+v", peerB)
	}
	if len(det.Data.NetNSSharers) != 1 || det.Data.NetNSSharers[0].ID != F {
		t.Errorf("A netns sharers = %+v", det.Data.NetNSSharers)
	}

	// Connecting C to n1 at runtime makes it reachable from A and B.
	if _, err := c.NetworkConnect(ctx, n1, client.NetworkConnectOptions{Container: cc}); err != nil {
		t.Fatal(err)
	}
	in.waitGraph("local", func(g model.Graph) string {
		if findLink(g, A, "reach", C) == nil || findLink(g, B, "reach", C) == nil {
			return "C not yet reachable from A and B"
		}
		return ""
	})
}
