package main

import (
	"context"
	"io"
	"log/slog"
	"net"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/moepig/fmlocal/internal/system/configfile"
)

func TestRunPortConflictStopsOtherProcesses(t *testing.T) {
	for _, failed := range []string{"aws api", "webui"} {
		t.Run(failed, func(t *testing.T) {
			listener, err := net.Listen("tcp", "127.0.0.1:0")
			if err != nil {
				t.Fatal(err)
			}
			defer listener.Close()
			port, err := strconv.Atoi(strings.TrimPrefix(listener.Addr().String(), "127.0.0.1:"))
			if err != nil {
				t.Fatal(err)
			}
			cfg := &configfile.Loaded{TickInterval: time.Hour}
			if failed == "aws api" {
				cfg.AWSAPIPort = port
			} else {
				cfg.WebUIPort = port
			}
			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			defer cancel()
			result := make(chan error, 1)
			go func() { result <- run(ctx, cfg, slog.New(slog.NewTextHandler(io.Discard, nil))) }()
			select {
			case err := <-result:
				if err == nil || !strings.Contains(err.Error(), failed) {
					t.Fatalf("run error = %v", err)
				}
			case <-ctx.Done():
				t.Fatal("run did not stop after port conflict")
			}
		})
	}
}
