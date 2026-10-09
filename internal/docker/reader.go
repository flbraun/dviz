// Package docker is dviz's only gateway to Docker daemons. Reader exposes the read calls
// dviz needs and sanitizes every result before returning it (see sanitize.go).
package docker

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/flbraun/dviz/internal/config"
	"github.com/moby/moby/api/types/container"
	"github.com/moby/moby/api/types/image"
	"github.com/moby/moby/api/types/network"
	"github.com/moby/moby/api/types/swarm"
	"github.com/moby/moby/api/types/system"
	"github.com/moby/moby/api/types/volume"
	"github.com/moby/moby/client"
)

// Reader is a read-only, sanitizing view of one Docker daemon.
type Reader struct {
	c *client.Client
}

// Open creates a Reader for the configured host. It does not contact the daemon.
func Open(h config.Host) (*Reader, error) {
	opts := []client.Opt{
		client.WithHost(h.URL),
		client.WithAPIVersionNegotiation(),
		client.WithUserAgent("dviz"),
	}
	if h.TLS != nil {
		tc, err := tlsConfig(h.TLS)
		if err != nil {
			return nil, err
		}
		opts = append(opts,
			client.WithHTTPClient(&http.Client{Transport: &http.Transport{TLSClientConfig: tc}}),
			client.WithScheme("https"),
		)
	}
	c, err := client.New(opts...)
	if err != nil {
		return nil, fmt.Errorf("docker client: %w", err)
	}
	return &Reader{c: c}, nil
}

func tlsConfig(t *config.TLS) (*tls.Config, error) {
	tc := &tls.Config{MinVersion: tls.VersionTLS12, InsecureSkipVerify: t.InsecureSkipVerify} //nolint:gosec // opt-in via config
	if t.CA != "" {
		pem, err := os.ReadFile(t.CA)
		if err != nil {
			return nil, fmt.Errorf("tls ca: %w", err)
		}
		pool := x509.NewCertPool()
		if !pool.AppendCertsFromPEM(pem) {
			return nil, errors.New("tls ca: no certificates found")
		}
		tc.RootCAs = pool
	}
	if t.Cert != "" {
		cert, err := tls.LoadX509KeyPair(t.Cert, t.Key)
		if err != nil {
			return nil, fmt.Errorf("tls cert: %w", err)
		}
		tc.Certificates = []tls.Certificate{cert}
	}
	return tc, nil
}

// Close releases the underlying client.
func (r *Reader) Close() error { return r.c.Close() }

// Version returns the engine version.
func (r *Reader) Version(ctx context.Context) (string, error) {
	v, err := r.c.ServerVersion(ctx, client.ServerVersionOptions{})
	if err != nil {
		return "", err
	}
	return v.Version, nil
}

// Info returns daemon-wide information.
func (r *Reader) Info(ctx context.Context) (system.Info, error) {
	res, err := r.c.Info(ctx, client.InfoOptions{})
	return res.Info, err
}

// Containers lists all containers, including stopped ones.
func (r *Reader) Containers(ctx context.Context) ([]container.Summary, error) {
	res, err := r.c.ContainerList(ctx, client.ContainerListOptions{All: true})
	return res.Items, err
}

// Container inspects one container.
func (r *Reader) Container(ctx context.Context, id string) (container.InspectResponse, error) {
	res, err := r.c.ContainerInspect(ctx, id, client.ContainerInspectOptions{})
	if err != nil {
		return container.InspectResponse{}, err
	}
	c := res.Container
	sanitizeContainerInspect(&c)
	return c, nil
}

// Networks lists all networks.
func (r *Reader) Networks(ctx context.Context) ([]network.Summary, error) {
	res, err := r.c.NetworkList(ctx, client.NetworkListOptions{})
	return res.Items, err
}

// Network inspects one network, including its local endpoints.
func (r *Reader) Network(ctx context.Context, id string) (network.Inspect, error) {
	res, err := r.c.NetworkInspect(ctx, id, client.NetworkInspectOptions{})
	return res.Network, err
}

// Volumes lists all volumes.
func (r *Reader) Volumes(ctx context.Context) ([]volume.Volume, error) {
	res, err := r.c.VolumeList(ctx, client.VolumeListOptions{})
	if err != nil {
		return nil, err
	}
	for i := range res.Items {
		sanitizeVolume(&res.Items[i])
	}
	return res.Items, nil
}

// Volume inspects one volume.
func (r *Reader) Volume(ctx context.Context, name string) (volume.Volume, error) {
	res, err := r.c.VolumeInspect(ctx, name, client.VolumeInspectOptions{})
	if err != nil {
		return volume.Volume{}, err
	}
	v := res.Volume
	sanitizeVolume(&v)
	return v, nil
}

// Images lists all top-level images.
func (r *Reader) Images(ctx context.Context) ([]image.Summary, error) {
	res, err := r.c.ImageList(ctx, client.ImageListOptions{})
	return res.Items, err
}

// Image inspects one image. Image history is deliberately not exposed: its CreatedBy
// entries can contain build ARG/ENV values.
func (r *Reader) Image(ctx context.Context, id string) (image.InspectResponse, error) {
	res, err := r.c.ImageInspect(ctx, id)
	if err != nil {
		return image.InspectResponse{}, err
	}
	i := res.InspectResponse
	sanitizeImageInspect(&i)
	return i, nil
}

// Services lists swarm services (manager nodes only).
func (r *Reader) Services(ctx context.Context) ([]swarm.Service, error) {
	res, err := r.c.ServiceList(ctx, client.ServiceListOptions{Status: true})
	if err != nil {
		return nil, err
	}
	for i := range res.Items {
		sanitizeService(&res.Items[i])
	}
	return res.Items, nil
}

// Tasks lists swarm tasks (manager nodes only).
func (r *Reader) Tasks(ctx context.Context) ([]swarm.Task, error) {
	res, err := r.c.TaskList(ctx, client.TaskListOptions{})
	if err != nil {
		return nil, err
	}
	for i := range res.Items {
		sanitizeTask(&res.Items[i])
	}
	return res.Items, nil
}

// Nodes lists swarm nodes (manager nodes only).
func (r *Reader) Nodes(ctx context.Context) ([]swarm.Node, error) {
	res, err := r.c.NodeList(ctx, client.NodeListOptions{})
	return res.Items, err
}

// Secrets lists swarm secrets without their data (manager nodes only).
func (r *Reader) Secrets(ctx context.Context) ([]swarm.Secret, error) {
	res, err := r.c.SecretList(ctx, client.SecretListOptions{})
	if err != nil {
		return nil, err
	}
	for i := range res.Items {
		sanitizeSecret(&res.Items[i])
	}
	return res.Items, nil
}

// Configs lists swarm configs without their data (manager nodes only).
func (r *Reader) Configs(ctx context.Context) ([]swarm.Config, error) {
	res, err := r.c.ConfigList(ctx, client.ConfigListOptions{})
	if err != nil {
		return nil, err
	}
	for i := range res.Items {
		sanitizeConfig(&res.Items[i])
	}
	return res.Items, nil
}

// Event is the minimal, attribute-free form of a daemon event. Raw event attributes and
// action suffixes (e.g. exec commands) are dropped at decode time.
type Event struct {
	Type   string
	Action string
	Actor  string
	Time   time.Time
}

// Events streams daemon events until ctx is done or the stream fails. The error channel
// receives exactly one value when the stream ends.
func (r *Reader) Events(ctx context.Context) (<-chan Event, <-chan error) {
	res := r.c.Events(ctx, client.EventsListOptions{})
	out := make(chan Event)
	errc := make(chan error, 1)
	go func() {
		defer close(out)
		for {
			select {
			case m, ok := <-res.Messages:
				if !ok {
					errc <- io.EOF
					return
				}
				action, _, _ := strings.Cut(string(m.Action), ":")
				ev := Event{Type: string(m.Type), Action: action, Actor: m.Actor.ID, Time: time.Unix(0, m.TimeNano)}
				select {
				case out <- ev:
				case <-ctx.Done():
					errc <- ctx.Err()
					return
				}
			case err := <-res.Err:
				if err == nil {
					err = io.EOF
				}
				errc <- err
				return
			case <-ctx.Done():
				errc <- ctx.Err()
				return
			}
		}
	}()
	return out, errc
}
