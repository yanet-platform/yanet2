package operator

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"time"
)

const (
	probeEndpoint        = "[::]:9903"
	probeShutdownTimeout = 5 * time.Second
	probeHeaderTimeout   = 5 * time.Second
)

func runProbeServer(ctx context.Context, ready func() bool) error {
	mux := http.NewServeMux()
	mux.HandleFunc(
		"GET /healthz",
		func(writer http.ResponseWriter, request *http.Request) {
			writer.WriteHeader(http.StatusOK)
		},
	)
	mux.HandleFunc(
		"GET /readyz",
		func(writer http.ResponseWriter, request *http.Request) {
			if ctx.Err() != nil || !ready() {
				http.Error(writer, "not ready", http.StatusServiceUnavailable)
				return
			}
			writer.WriteHeader(http.StatusOK)
		},
	)
	server := &http.Server{
		Addr:              probeEndpoint,
		Handler:           mux,
		ReadHeaderTimeout: probeHeaderTimeout,
	}
	serveErrors := make(chan error, 1)
	go func() {
		serveErrors <- server.ListenAndServe()
	}()

	select {
	case err := <-serveErrors:
		return fmt.Errorf("failed to serve probes on %q: %w", probeEndpoint, err)
	case <-ctx.Done():
	}

	shutdownCtx, cancel := context.WithTimeout(context.Background(), probeShutdownTimeout)
	defer cancel()
	shutdownErr := server.Shutdown(shutdownCtx)
	if shutdownErr != nil {
		shutdownErr = errors.Join(shutdownErr, server.Close())
		shutdownErr = fmt.Errorf("failed to stop probes: %w", shutdownErr)
	}
	serveErr := <-serveErrors
	if errors.Is(serveErr, http.ErrServerClosed) {
		serveErr = nil
	} else if serveErr != nil {
		serveErr = fmt.Errorf("failed to serve probes on %q: %w", probeEndpoint, serveErr)
	}
	return errors.Join(shutdownErr, serveErr)
}
