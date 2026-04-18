// Package awsapi is the inbound adapter that exposes the matchmaking
// application service as an AWS GameLift-compatible JSON-RPC endpoint.
package awsapi

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"time"

	appmm "github.com/moepig/fmlocal/internal/app/matchmaking"
)

// Options holds server-level settings not owned by the application service.
type Options struct {
	AWSAPIPort int
}

// Server is the inbound HTTP adapter. All business logic is delegated to
// the application-layer Service.
type Server struct {
	service *appmm.Service
	options Options
	logger  *slog.Logger
}

func NewServer(service *appmm.Service, opts Options, logger *slog.Logger) *Server {
	if logger == nil {
		logger = slog.Default()
	}
	return &Server{service: service, options: opts, logger: logger}
}

func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("POST /", s.dispatch)
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	})
	return mux
}

func (s *Server) Run(ctx context.Context) error {
	srv := &http.Server{
		Addr:              fmt.Sprintf(":%d", s.options.AWSAPIPort),
		Handler:           s.Handler(),
		ReadHeaderTimeout: 5 * time.Second,
	}
	errCh := make(chan error, 1)
	go func() { errCh <- srv.ListenAndServe() }()
	select {
	case <-ctx.Done():
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		return srv.Shutdown(shutdownCtx)
	case err := <-errCh:
		return err
	}
}
