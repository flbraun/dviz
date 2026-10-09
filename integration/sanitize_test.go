package integration

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/flbraun/dviz/internal/config"
	"github.com/flbraun/dviz/internal/docker"
	"github.com/flbraun/dviz/internal/model"
	"github.com/moby/moby/api/types/container"
	"github.com/moby/moby/api/types/mount"
	"github.com/moby/moby/api/types/swarm"
	"github.com/moby/moby/client"
)

// TestSecretsNeverLeave plants a unique secret in every place Docker can return it and
// asserts that no byte of any API response, or of the live graph stream, contains it.
func TestSecretsNeverLeave(t *testing.T) {
	c := daemon(t)
	dh := dind(t)
	ctx := context.Background()
	secret := "s3cr3t-" + runID + "-value"
	leaks := []string{secret, base64.StdEncoding.EncodeToString([]byte(secret))}

	// Local daemon: container env, image ENV, log options, volume driver options and
	// healthcheck output.
	vol, err := c.VolumeCreate(ctx, client.VolumeCreateOptions{
		Name: name(t, "secret-vol"), Driver: "local", Labels: labels(nil),
		DriverOpts: map[string]string{"type": "tmpfs", "device": "tmpfs", "o": "size=1m,uid=" + secret},
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, _ = c.VolumeRemove(context.Background(), vol.Volume.Name, client.VolumeRemoveOptions{Force: true})
	})

	base := run(t, c, ctr{Name: name(t, "secret-base"), NoStart: true})
	commit, err := c.ContainerCommit(ctx, base, client.ContainerCommitOptions{
		Reference: "dvizt-" + runID + "-secret:latest",
		Config:    &container.Config{Image: testImage, Env: []string{"IMAGE_TOKEN=" + secret}, Cmd: []string{"sleep", "3600"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _, _ = c.ImageRemove(context.Background(), commit.ID, client.ImageRemoveOptions{Force: true}) })

	id := run(t, c, ctr{
		Name:  name(t, "secret"),
		Image: "dvizt-" + runID + "-secret:latest",
		Env:   []string{"DB_PASSWORD=" + secret, "PLAIN=1"},
		Host: &container.HostConfig{
			LogConfig: container.LogConfig{Type: "json-file", Config: map[string]string{"tag": secret}},
		},
		// The probe prints the env value, so only its output (not its command) holds the secret.
		Health: &container.HealthConfig{Test: []string{"CMD-SHELL", "echo $DB_PASSWORD"}, Interval: time.Second, StartPeriod: time.Second},
	})
	// Never started: the bogus mount option would fail at mount time.
	mounted := run(t, c, ctr{Name: name(t, "secret-mount"), NoStart: true, Host: &container.HostConfig{
		Mounts: []mount.Mount{{Type: mount.TypeVolume, Target: "/anon", VolumeOptions: &mount.VolumeOptions{
			DriverConfig: &mount.Driver{Name: "local", Options: map[string]string{"type": "tmpfs", "device": "tmpfs", "o": "size=1m," + secret}},
		}}},
	}})

	// dind daemon (swarm manager): service env, secret and config payloads.
	sec, err := dh.Client.SecretCreate(ctx, client.SecretCreateOptions{Spec: swarm.SecretSpec{Annotations: swarm.Annotations{Name: name(t, "secret")}, Data: []byte(secret)}})
	if err != nil {
		t.Fatal(err)
	}
	cfg, err := dh.Client.ConfigCreate(ctx, client.ConfigCreateOptions{Spec: swarm.ConfigSpec{Annotations: swarm.Annotations{Name: name(t, "config")}, Data: []byte(secret)}})
	if err != nil {
		t.Fatal(err)
	}
	svc, err := dh.Client.ServiceCreate(ctx, client.ServiceCreateOptions{Spec: swarm.ServiceSpec{
		Annotations: swarm.Annotations{Name: name(t, "svc")},
		TaskTemplate: swarm.TaskSpec{
			ContainerSpec: &swarm.ContainerSpec{
				Image:   testImage,
				Command: []string{"sleep", "3600"},
				Env:     []string{"SERVICE_TOKEN=" + secret},
				Secrets: []*swarm.SecretReference{{SecretID: sec.ID, SecretName: name(t, "secret"),
					File: &swarm.SecretReferenceFileTarget{Name: "token", UID: "0", GID: "0", Mode: 0o400}}},
				Configs: []*swarm.ConfigReference{{ConfigID: cfg.ID, ConfigName: name(t, "config"),
					File: &swarm.ConfigReferenceFileTarget{Name: "/cfg", UID: "0", GID: "0", Mode: 0o444}}},
			},
			LogDriver: &swarm.Driver{Name: "json-file", Options: map[string]string{"tag": secret}},
		},
	}})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		ctx := context.Background()
		_, _ = dh.Client.ServiceRemove(ctx, svc.ID, client.ServiceRemoveOptions{})
		time.Sleep(time.Second)
		_, _ = dh.Client.SecretRemove(ctx, sec.ID, client.SecretRemoveOptions{})
		_, _ = dh.Client.ConfigRemove(ctx, cfg.ID, client.ConfigRemoveOptions{})
	})

	in := start(t, localYAML()+dindYAML(dh, "dind"))
	in.waitConnected("local", "dind")
	stream := in.stream("/api/hosts/local/graph/events")

	in.waitGraph("local", func(g model.Graph) string {
		if !hasNode(g, docker.ContainerID(id)) || !hasNode(g, docker.ContainerID(mounted)) {
			return "secret containers missing"
		}
		return ""
	})
	in.waitGraph("dind", func(g model.Graph) string {
		for _, n := range []string{docker.ServiceID(svc.ID), docker.SecretID(sec.ID), docker.ConfigID(cfg.ID)} {
			if !hasNode(g, n) {
				return "missing " + n
			}
		}
		return ""
	})
	// Let the healthcheck run so its output would be present in inspect data.
	time.Sleep(2500 * time.Millisecond)

	var bodies []string
	bodies = append(bodies, in.raw("/api/hosts"))
	for _, host := range []string{"local", "dind"} {
		g := in.graph(host)
		bodies = append(bodies, in.raw("/api/hosts/"+host+"/graph"))
		for _, n := range g.Nodes {
			bodies = append(bodies, in.raw(entityPath(host, n.ID)))
		}
	}
	// Everything the live stream delivered while fixtures were created and changed.
	drain := time.After(500 * time.Millisecond)
collect:
	for {
		select {
		case ev := <-stream:
			bodies = append(bodies, ev.Data)
		case <-drain:
			break collect
		}
	}

	for _, body := range bodies {
		for _, leak := range leaks {
			if strings.Contains(body, leak) {
				t.Fatalf("secret leaked in response: …%s…", around(body, leak))
			}
		}
	}

	// The read boundary itself: nothing returned by docker.Reader carries the secret, even
	// fields the API's DTOs never expose.
	readerOutputs := func(h config.Host) map[string]any {
		r, err := docker.Open(h)
		if err != nil {
			t.Fatal(err)
		}
		defer r.Close()
		out := map[string]any{}
		add := func(k string, v any, err error) {
			if err != nil {
				t.Fatalf("%s %s: %v", h.Name, k, err)
			}
			out[k] = v
		}
		info, err := r.Info(ctx)
		add("info", info, err)
		cs, err := r.Containers(ctx)
		add("containers", cs, err)
		for _, c := range cs {
			ci, err := r.Container(ctx, c.ID)
			add("container "+c.ID, ci, err)
		}
		vs, err := r.Volumes(ctx)
		add("volumes", vs, err)
		for _, v := range vs {
			vi, err := r.Volume(ctx, v.Name)
			add("volume "+v.Name, vi, err)
		}
		is, err := r.Images(ctx)
		add("images", is, err)
		for _, i := range is {
			ii, err := r.Image(ctx, i.ID)
			add("image "+i.ID, ii, err)
		}
		if info.Swarm.ControlAvailable {
			svcs, err := r.Services(ctx)
			add("services", svcs, err)
			tasks, err := r.Tasks(ctx)
			add("tasks", tasks, err)
			secs, err := r.Secrets(ctx)
			add("secrets", secs, err)
			cfgs, err := r.Configs(ctx)
			add("configs", cfgs, err)
		}
		return out
	}
	for _, h := range []config.Host{
		{Name: "local", URL: localURL()},
		{Name: "dind", URL: dh.URL, TLS: &config.TLS{CA: dh.CertDir + "/ca.pem", Cert: dh.CertDir + "/cert.pem", Key: dh.CertDir + "/key.pem"}},
	} {
		for k, v := range readerOutputs(h) {
			b, err := json.Marshal(v)
			if err != nil {
				t.Fatal(err)
			}
			for _, leak := range leaks {
				if strings.Contains(string(b), leak) {
					t.Errorf("docker.Reader %s/%s returned the secret: …%s…", h.Name, k, around(string(b), leak))
				}
			}
		}
	}

	// Keys stay visible.
	var det struct {
		Data docker.ContainerDetails `json:"data"`
	}
	in.get(entityPath("local", docker.ContainerID(id)), &det)
	for _, k := range []string{"DB_PASSWORD", "PLAIN", "IMAGE_TOKEN"} {
		if !strings.Contains(strings.Join(det.Data.Env, ","), k) {
			t.Errorf("env keys %v lack %s", det.Data.Env, k)
		}
	}
	var svcDet struct {
		Data docker.ServiceDetails `json:"data"`
	}
	in.get(entityPath("dind", docker.ServiceID(svc.ID)), &svcDet)
	if len(svcDet.Data.Env) != 1 || svcDet.Data.Env[0] != "SERVICE_TOKEN" || len(svcDet.Data.Secrets) != 1 {
		t.Errorf("service details = %+v", svcDet.Data)
	}
}

// around returns the text surrounding the first occurrence of needle.
func around(s, needle string) string {
	i := strings.Index(s, needle)
	return s[max(0, i-200):min(len(s), i+len(needle)+50)]
}
