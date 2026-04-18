package matchmaking

import (
	"context"
	"log/slog"
	"sync"
	"time"

	mm "github.com/moepig/fmlocal/internal/domain/matchmaking"
)

type Ticker struct {
	Service *Service
	Names   []mm.ConfigurationName
	Logger  *slog.Logger
}

func (t *Ticker) Run(ctx context.Context, interval time.Duration) error {
	if interval <= 0 {
		interval = time.Second
	}
	logger := t.Logger
	if logger == nil {
		logger = slog.Default()
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
			var wg sync.WaitGroup
			for _, name := range t.Names {
				wg.Add(1)
				go func(name mm.ConfigurationName) {
					defer wg.Done()
					if err := t.Service.Tick(ctx, name); err != nil {
						logger.Error("matchmaking tick failed", "configuration", name, "error", err.Error())
					}
				}(name)
			}
			wg.Wait()
		}
	}
}
