package engine

import (
	"bufio"
	"context"
	"os"
	"strconv"
	"strings"
	"time"
)

type sampledHostTelemetry struct {
	Available            bool
	CPUUsagePercent      float64
	MemoryUsedBytes      int64
	MemoryAvailableBytes int64
	Load1                float64
	Load5                float64
	Load15               float64
	ObservedAt           time.Time
}

type procSample struct {
	total           uint64
	idle            uint64
	memoryTotal     int64
	memoryAvailable int64
	load1           float64
	load5           float64
	load15          float64
}

func (s *Server) sampleHostTelemetry(ctx context.Context) {
	defer close(s.telemetryDone)
	var previous procSample
	var havePrevious bool
	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()
	for {
		current, err := readProcSample()
		if err == nil {
			telemetry := sampledHostTelemetry{
				MemoryUsedBytes:      current.memoryTotal - current.memoryAvailable,
				MemoryAvailableBytes: current.memoryAvailable,
				Load1:                current.load1,
				Load5:                current.load5,
				Load15:               current.load15,
				ObservedAt:           time.Now().UTC(),
			}
			if havePrevious {
				telemetry.CPUUsagePercent, telemetry.Available = hostCPUPercent(previous, current)
			}
			previous = current
			havePrevious = true
			s.telemetryMu.Lock()
			s.hostTelemetry = telemetry
			s.telemetryMu.Unlock()
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

func hostCPUPercent(previous, current procSample) (float64, bool) {
	if current.total <= previous.total || current.idle < previous.idle {
		return 0, false
	}
	totalDelta := current.total - previous.total
	idleDelta := current.idle - previous.idle
	if idleDelta > totalDelta {
		return 0, false
	}
	return float64(totalDelta-idleDelta) / float64(totalDelta) * 100, true
}

func readProcSample() (procSample, error) {
	var result procSample
	cpu, err := os.Open("/proc/stat")
	if err != nil {
		return result, err
	}
	scanner := bufio.NewScanner(cpu)
	if scanner.Scan() {
		fields := strings.Fields(scanner.Text())
		if len(fields) >= 5 && fields[0] == "cpu" {
			for index, field := range fields[1:] {
				value, parseErr := strconv.ParseUint(field, 10, 64)
				if parseErr != nil {
					cpu.Close()
					return result, parseErr
				}
				result.total += value
				if index == 3 || index == 4 {
					result.idle += value
				}
			}
		}
	}
	err = scanner.Err()
	cpu.Close()
	if err != nil {
		return result, err
	}

	memory, err := os.Open("/proc/meminfo")
	if err != nil {
		return result, err
	}
	scanner = bufio.NewScanner(memory)
	for scanner.Scan() {
		fields := strings.Fields(scanner.Text())
		if len(fields) < 2 {
			continue
		}
		value, parseErr := strconv.ParseInt(fields[1], 10, 64)
		if parseErr != nil {
			continue
		}
		switch strings.TrimSuffix(fields[0], ":") {
		case "MemTotal":
			result.memoryTotal = value * 1024
		case "MemAvailable":
			result.memoryAvailable = value * 1024
		}
	}
	err = scanner.Err()
	memory.Close()
	if err != nil {
		return result, err
	}

	load, err := os.ReadFile("/proc/loadavg")
	if err != nil {
		return result, err
	}
	fields := strings.Fields(string(load))
	if len(fields) >= 3 {
		result.load1, _ = strconv.ParseFloat(fields[0], 64)
		result.load5, _ = strconv.ParseFloat(fields[1], 64)
		result.load15, _ = strconv.ParseFloat(fields[2], 64)
	}
	return result, nil
}
