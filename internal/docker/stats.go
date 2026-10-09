package docker

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"strings"

	"github.com/moby/moby/api/types/container"
	"github.com/moby/moby/client"
)

// StatsSample is one computed resource usage sample of a container.
type StatsSample struct {
	TS       int64   `json:"ts"` // unix milliseconds
	CPUPct   float64 `json:"cpuPct"`
	MemUsage uint64  `json:"memUsage"`
	MemLimit uint64  `json:"memLimit"`
	NetRx    uint64  `json:"netRx"`
	NetTx    uint64  `json:"netTx"`
	BlkRead  uint64  `json:"blkRead"`
	BlkWrite uint64  `json:"blkWrite"`
	PIDs     uint64  `json:"pids"`
}

// Stats streams computed samples (about one per second) until ctx is done or emit fails.
func (r *Reader) Stats(ctx context.Context, id string, emit func(StatsSample) error) error {
	res, err := r.c.ContainerStats(ctx, id, client.ContainerStatsOptions{Stream: true})
	if err != nil {
		return err
	}
	defer res.Body.Close()

	dec := json.NewDecoder(res.Body)
	for {
		var s container.StatsResponse
		if err := dec.Decode(&s); err != nil {
			if errors.Is(err, io.EOF) || ctx.Err() != nil {
				return nil
			}
			return err
		}
		if err := emit(computeSample(&s)); err != nil {
			return err
		}
	}
}

func computeSample(s *container.StatsResponse) StatsSample {
	out := StatsSample{
		TS:       s.Read.UnixMilli(),
		MemLimit: s.MemoryStats.Limit,
		PIDs:     s.PidsStats.Current,
	}

	cpuDelta := float64(s.CPUStats.CPUUsage.TotalUsage) - float64(s.PreCPUStats.CPUUsage.TotalUsage)
	sysDelta := float64(s.CPUStats.SystemUsage) - float64(s.PreCPUStats.SystemUsage)
	cpus := float64(s.CPUStats.OnlineCPUs)
	if cpus == 0 {
		cpus = float64(len(s.CPUStats.CPUUsage.PercpuUsage))
	}
	if cpuDelta > 0 && sysDelta > 0 && cpus > 0 {
		out.CPUPct = cpuDelta / sysDelta * cpus * 100
	}

	// Match `docker stats`: exclude reclaimable page cache from usage.
	usage := s.MemoryStats.Usage
	inactive, ok := s.MemoryStats.Stats["inactive_file"] // cgroup v2
	if !ok {
		inactive = s.MemoryStats.Stats["total_inactive_file"] // cgroup v1
	}
	if inactive < usage {
		usage -= inactive
	}
	out.MemUsage = usage

	for _, n := range s.Networks {
		out.NetRx += n.RxBytes
		out.NetTx += n.TxBytes
	}
	for _, e := range s.BlkioStats.IoServiceBytesRecursive {
		switch strings.ToLower(e.Op) {
		case "read":
			out.BlkRead += e.Value
		case "write":
			out.BlkWrite += e.Value
		}
	}
	return out
}
