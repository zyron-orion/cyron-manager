package download

import (
	"context"
	"time"

	"cyronmanager/internal/aria2"
)

type ariaEngine interface {
	AddURI(uris []string, options map[string]string) (string, error)
	AddTorrent(torrentBase64 string, options map[string]string) (string, error)
	TellStatus(gid string) (aria2.Status, error)
	Pause(gid string) error
	Unpause(gid string) error
	Remove(gid string) error
	RemoveDownloadResult(gid string) error
	SetConnections(connections int) error
	SetMaxConcurrent(n int) error
	Stop()
}

const defaultAriaIdleTTL = 3 * time.Minute

func startAriaEngine(ctx context.Context, bin string, opts aria2.Options) (ariaEngine, error) {
	return aria2.Start(ctx, bin, opts)
}
