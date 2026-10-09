package docker

import (
	"strings"

	"github.com/moby/moby/api/types/container"
	"github.com/moby/moby/api/types/image"
	"github.com/moby/moby/api/types/mount"
	"github.com/moby/moby/api/types/swarm"
	"github.com/moby/moby/api/types/volume"
)

// The functions in this file run on every value decoded from a Docker daemon before it is
// returned by Reader. They drop environment variable values and secret-bearing payloads
// (secret/config data, driver and log options) so no other code can see or leak them.

// envKeys reduces "KEY=value" entries to "KEY".
func envKeys(env []string) []string {
	if env == nil {
		return nil
	}
	out := make([]string, len(env))
	for i, e := range env {
		k, _, _ := strings.Cut(e, "=")
		out[i] = k
	}
	return out
}

// optionKeys blanks all values of a driver/log options map, keeping the keys.
func optionKeys(m map[string]string) map[string]string {
	if m == nil {
		return nil
	}
	out := make(map[string]string, len(m))
	for k := range m {
		out[k] = ""
	}
	return out
}

func sanitizeDriver(d *swarm.Driver) {
	if d != nil {
		d.Options = optionKeys(d.Options)
	}
}

func sanitizeMounts(ms []mount.Mount) {
	for i := range ms {
		if vo := ms[i].VolumeOptions; vo != nil && vo.DriverConfig != nil {
			vo.DriverConfig.Options = optionKeys(vo.DriverConfig.Options)
		}
	}
}

func sanitizeContainerInspect(c *container.InspectResponse) {
	if c.Config != nil {
		c.Config.Env = envKeys(c.Config.Env)
	}
	if hc := c.HostConfig; hc != nil {
		hc.LogConfig.Config = optionKeys(hc.LogConfig.Config)
		sanitizeMounts(hc.Mounts)
	}
	if c.State != nil && c.State.Health != nil {
		// Healthcheck output is command output that may echo secrets; keep only exit codes.
		for _, r := range c.State.Health.Log {
			if r != nil {
				r.Output = ""
			}
		}
	}
}

func sanitizeImageInspect(i *image.InspectResponse) {
	if i.Config != nil {
		i.Config.Env = envKeys(i.Config.Env)
	}
}

func sanitizeVolume(v *volume.Volume) {
	v.Options = optionKeys(v.Options)
	v.Status = nil
}

func sanitizeTaskSpec(s *swarm.TaskSpec) {
	if s == nil {
		return
	}
	if cs := s.ContainerSpec; cs != nil {
		cs.Env = envKeys(cs.Env)
		sanitizeMounts(cs.Mounts)
	}
	if ps := s.PluginSpec; ps != nil {
		ps.Env = envKeys(ps.Env)
	}
	sanitizeDriver(s.LogDriver)
	for i := range s.Networks {
		s.Networks[i].DriverOpts = optionKeys(s.Networks[i].DriverOpts)
	}
}

func sanitizeService(s *swarm.Service) {
	sanitizeTaskSpec(&s.Spec.TaskTemplate)
	if s.PreviousSpec != nil {
		sanitizeTaskSpec(&s.PreviousSpec.TaskTemplate)
	}
}

func sanitizeTask(t *swarm.Task) {
	sanitizeTaskSpec(&t.Spec)
}

func sanitizeSecret(s *swarm.Secret) {
	s.Spec.Data = nil
	sanitizeDriver(s.Spec.Driver)
	sanitizeDriver(s.Spec.Templating)
}

func sanitizeConfig(c *swarm.Config) {
	c.Spec.Data = nil
	sanitizeDriver(c.Spec.Templating)
}
