//go:build !windows

package engine

import (
	"syscall"

	"github.com/dockside-gg/game-panel/internal/engineclient"
)

func filesystemUsage(path string) (engineclient.FilesystemUsage, error) {
	var stats syscall.Statfs_t
	if err := syscall.Statfs(path, &stats); err != nil {
		return engineclient.FilesystemUsage{}, err
	}
	total := int64(stats.Blocks) * int64(stats.Bsize)
	free := int64(stats.Bavail) * int64(stats.Bsize)
	return engineclient.FilesystemUsage{
		Path: path, TotalBytes: total, UsedBytes: total - free, FreeBytes: free,
	}, nil
}
