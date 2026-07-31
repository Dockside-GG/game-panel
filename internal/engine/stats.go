package engine

import (
	"context"
	"encoding/json"
	"net/http"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/docker/docker/api/types"
	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/api/types/filters"
	"github.com/dockside-gg/game-panel/internal/engineclient"
)

func (s *Server) serverStats(w http.ResponseWriter, r *http.Request) {
	filter := filters.NewArgs(
		filters.Arg("label", "gg.dockside.managed=true"),
		filters.Arg("label", "gg.dockside.instance="+s.cfg.InstanceID),
		filters.Arg("label", "gg.dockside.kind=server"),
	)
	managed, err := s.docker.ContainerList(r.Context(), container.ListOptions{All: true, Filters: filter})
	if err != nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "could not list managed containers"})
		return
	}

	result := make([]engineclient.ServerStats, len(managed))
	semaphore := make(chan struct{}, 8)
	var group sync.WaitGroup
	for index, summary := range managed {
		index, summary := index, summary
		group.Add(1)
		go func() {
			defer group.Done()
			semaphore <- struct{}{}
			defer func() { <-semaphore }()
			result[index] = s.readServerStats(r.Context(), summary)
		}()
	}
	group.Wait()
	sort.Slice(result, func(i, j int) bool { return result[i].ServerID < result[j].ServerID })
	writeJSON(w, http.StatusOK, map[string]any{
		"servers":     result,
		"observed_at": time.Now().UTC(),
	})
}

func (s *Server) readServerStats(parent context.Context, summary types.Container) engineclient.ServerStats {
	observed := engineclient.ServerStats{
		ServerID:    summary.Labels["gg.dockside.server"],
		ContainerID: summary.ID,
		State:       summary.State,
		Health:      "unknown",
		ObservedAt:  time.Now().UTC(),
	}
	ctx, cancel := context.WithTimeout(parent, 5*time.Second)
	defer cancel()
	inspected, err := s.docker.ContainerInspect(ctx, summary.ID)
	if err != nil {
		observed.Error = "container inspection failed"
		return observed
	}
	if inspected.State != nil {
		observed.State = inspected.State.Status
		if inspected.State.Health != nil {
			observed.Health = inspected.State.Health.Status
		}
		if inspected.State.StartedAt != "" {
			if started, err := time.Parse(time.RFC3339Nano, inspected.State.StartedAt); err == nil && !started.IsZero() {
				observed.StartedAt = &started
			}
		}
		if !inspected.State.Running {
			code := inspected.State.ExitCode
			observed.ExitCode = &code
		}
	}
	if observed.State != "running" {
		return observed
	}
	response, err := s.docker.ContainerStatsOneShot(ctx, summary.ID)
	if err != nil {
		observed.Error = "container stats unavailable"
		return observed
	}
	defer response.Body.Close()
	var stats container.StatsResponse
	if err := json.NewDecoder(response.Body).Decode(&stats); err != nil {
		observed.Error = "container stats decode failed"
		return observed
	}
	observed.CPUPercent = calculateCPU(stats)
	usage := stats.MemoryStats.Usage
	if cache := stats.MemoryStats.Stats["inactive_file"]; usage > cache {
		usage -= cache
	}
	observed.MemoryBytes = int64(usage)
	observed.MemoryLimitBytes = int64(stats.MemoryStats.Limit)
	for _, network := range stats.Networks {
		observed.NetworkRXBytes += int64(network.RxBytes)
		observed.NetworkTXBytes += int64(network.TxBytes)
	}
	for _, entry := range stats.BlkioStats.IoServiceBytesRecursive {
		switch strings.ToLower(entry.Op) {
		case "read":
			observed.BlockReadBytes += int64(entry.Value)
		case "write":
			observed.BlockWriteBytes += int64(entry.Value)
		}
	}
	if stats.StorageStats.ReadSizeBytes > 0 {
		observed.BlockReadBytes = int64(stats.StorageStats.ReadSizeBytes)
	}
	if stats.StorageStats.WriteSizeBytes > 0 {
		observed.BlockWriteBytes = int64(stats.StorageStats.WriteSizeBytes)
	}
	observed.Pids = int64(stats.PidsStats.Current)
	return observed
}

func calculateCPU(stats container.StatsResponse) float64 {
	if stats.CPUStats.CPUUsage.TotalUsage < stats.PreCPUStats.CPUUsage.TotalUsage ||
		stats.CPUStats.SystemUsage < stats.PreCPUStats.SystemUsage {
		return 0
	}
	cpuDelta := stats.CPUStats.CPUUsage.TotalUsage - stats.PreCPUStats.CPUUsage.TotalUsage
	systemDelta := stats.CPUStats.SystemUsage - stats.PreCPUStats.SystemUsage
	online := stats.CPUStats.OnlineCPUs
	if online == 0 {
		online = uint32(len(stats.CPUStats.CPUUsage.PercpuUsage))
	}
	if cpuDelta == 0 || systemDelta == 0 || online == 0 {
		return 0
	}
	value := (float64(cpuDelta) / float64(systemDelta)) * float64(online) * 100
	if value < 0 || value != value {
		return 0
	}
	return value
}
