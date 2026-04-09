package main

import (
	"context"
	"flag"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/scttfrdmn/ember/pkg/hearth"
	"github.com/scttfrdmn/ember/pkg/sdk"
	"github.com/scttfrdmn/ember/pkg/serve"
)

func runServe(args []string) error {
	fs := flag.NewFlagSet("serve", flag.ContinueOnError)
	port := fs.Int("port", 8080, "listen port")
	maxPages := fs.Uint("max-memory-pages", 256, "max WASM memory pages (64KB each)")
	verbose := fs.Bool("verbose", false, "log requests to stderr")

	if err := fs.Parse(args); err != nil {
		return err
	}

	// Use auto-detected caps but override max memory pages.
	autoCaps := hearth.New().Capabilities()
	autoCaps.MaxMemoryPages = uint32(*maxPages)
	s := sdk.NewWithCaps(autoCaps)

	srv := serve.New(s)

	if *verbose {
		srv = serve.NewWithMiddleware(s, loggingMiddleware)
	}

	addr := fmt.Sprintf(":%d", *port)
	fmt.Fprintf(os.Stderr, "ember serve: listening on %s\n", addr)

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	errCh := make(chan error, 1)
	go func() {
		if err := srv.ListenAndServe(addr); err != nil && err != http.ErrServerClosed {
			errCh <- err
		}
	}()

	select {
	case err := <-errCh:
		return fmt.Errorf("server error: %w", err)
	case <-ctx.Done():
		fmt.Fprintf(os.Stderr, "ember serve: shutting down\n")
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		return srv.Shutdown(shutdownCtx)
	}
}

// loggingMiddleware wraps an http.Handler to log METHOD /path → status elapsed.
func loggingMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		rw := &responseWriter{ResponseWriter: w, status: http.StatusOK}
		next.ServeHTTP(rw, r)
		fmt.Fprintf(os.Stderr, "[%s] %s → %d (%s)\n",
			r.Method, r.URL.Path, rw.status, time.Since(start))
	})
}

// responseWriter wraps http.ResponseWriter to capture the status code.
type responseWriter struct {
	http.ResponseWriter
	status int
}

func (rw *responseWriter) WriteHeader(code int) {
	rw.status = code
	rw.ResponseWriter.WriteHeader(code)
}
