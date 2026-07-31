//go:build windows

package engine

import (
	"errors"

	"github.com/dockside-gg/game-panel/internal/engineclient"
)

func filesystemUsage(string) (engineclient.FilesystemUsage, error) {
	return engineclient.FilesystemUsage{}, errors.New("Docker-host filesystem telemetry is available inside the Linux engine container")
}
