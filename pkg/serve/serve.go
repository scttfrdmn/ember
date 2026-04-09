// Package serve provides a minimal HTTP server wrapping pkg/sdk.
// Uses only stdlib net/http — no external framework dependencies.
package serve

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"time"

	"github.com/scttfrdmn/ember/core/intent"
	"github.com/scttfrdmn/ember/pkg/runtime"
	"github.com/scttfrdmn/ember/pkg/sdk"
)

const version = "0.5.0"

// Server is an HTTP server wrapping an SDK instance.
type Server struct {
	sdk *sdk.SDK
	mux *http.ServeMux
	srv *http.Server // held as *http.Server (not package-level func) to support Shutdown
}

// New creates a Server backed by the given SDK.
// All routes are registered in New; call ListenAndServe to start accepting connections.
func New(s *sdk.SDK) *Server {
	srv := &Server{sdk: s, mux: http.NewServeMux()}
	srv.srv = &http.Server{Handler: srv.mux}
	srv.registerRoutes()
	return srv
}

// NewWithMiddleware creates a Server like New, but wraps the mux with the
// given middleware (e.g. a request logger). The middleware receives the full
// mux as its inner handler.
func NewWithMiddleware(s *sdk.SDK, mw func(http.Handler) http.Handler) *Server {
	srv := &Server{sdk: s, mux: http.NewServeMux()}
	srv.srv = &http.Server{Handler: mw(srv.mux)}
	srv.registerRoutes()
	return srv
}

// Handler returns the HTTP handler for the server (useful for httptest in tests).
func (s *Server) Handler() http.Handler {
	return s.srv.Handler
}

// ListenAndServe starts listening on addr (e.g. ":8080").
// Blocks until the server is stopped or returns an error.
func (s *Server) ListenAndServe(addr string) error {
	s.srv.Addr = addr
	return s.srv.ListenAndServe()
}

// Shutdown gracefully stops the server. In-flight requests are given ctx to drain.
func (s *Server) Shutdown(ctx context.Context) error {
	return s.srv.Shutdown(ctx)
}

func (s *Server) registerRoutes() {
	s.mux.HandleFunc("GET /health", s.handleHealth)
	s.mux.HandleFunc("GET /capabilities", s.handleCapabilities)
	s.mux.HandleFunc("POST /build", s.handleBuild)
	s.mux.HandleFunc("POST /burn", s.handleBurn)
	s.mux.HandleFunc("POST /batch", s.handleBatch)
}

// --- Route handlers ---

func (s *Server) handleHealth(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok", "version": version})
}

func (s *Server) handleCapabilities(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, s.sdk.Hearth().Capabilities())
}

// buildRequest is the POST /build request body.
type buildRequest struct {
	SourceB64 string `json:"source_b64"` // base64-encoded Go source file
	Filename  string `json:"filename"`   // optional; defaults to "ember.go"
}

// buildResponse is the POST /build response body.
type buildResponse struct {
	WASMB64  string      `json:"wasm_b64"`
	Manifest interface{} `json:"manifest"`
	Exports  interface{} `json:"exports"`
}

func (s *Server) handleBuild(w http.ResponseWriter, r *http.Request) {
	var req buildRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON: "+err.Error())
		return
	}
	sourceBytes, err := base64.StdEncoding.DecodeString(req.SourceB64)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid base64 source: "+err.Error())
		return
	}

	filename := req.Filename
	if filename == "" {
		filename = "ember.go"
	}

	// Write to a temp directory; loader.LoadDir requires a real filesystem path.
	dir, err := os.MkdirTemp("", "ember-build-*")
	if err != nil {
		writeError(w, http.StatusInternalServerError, "temp dir: "+err.Error())
		return
	}
	defer os.RemoveAll(dir)

	if err := os.WriteFile(dir+"/"+filename, sourceBytes, 0o600); err != nil {
		writeError(w, http.StatusInternalServerError, "write source: "+err.Error())
		return
	}
	// Write a minimal go.mod so that loader.LoadDir can resolve the package.
	gomod := "module ember.local/build\n\ngo 1.21\n"
	if err := os.WriteFile(dir+"/go.mod", []byte(gomod), 0o600); err != nil {
		writeError(w, http.StatusInternalServerError, "write go.mod: "+err.Error())
		return
	}

	artifact, err := s.sdk.Build(r.Context(), dir)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "build: "+err.Error())
		return
	}

	writeJSON(w, http.StatusOK, buildResponse{
		WASMB64:  base64.StdEncoding.EncodeToString(artifact.WASM),
		Manifest: artifact.Manifest,
		Exports:  artifact.Exports,
	})
}

// burnRequest is the POST /burn request body.
type burnRequest struct {
	WASMB64     string                 `json:"wasm_b64"`
	ManifestRaw json.RawMessage        `json:"manifest"`
	Fn          string                 `json:"fn"`
	Args        []uint64               `json:"args"`
}

// burnResponse is the POST /burn response body.
type burnResponse struct {
	Results   []uint64 `json:"results"`
	ElapsedMs float64  `json:"elapsed_ms"`
}

func (s *Server) handleBurn(w http.ResponseWriter, r *http.Request) {
	var req burnRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON: "+err.Error())
		return
	}

	artifact, err := decodeArtifact(req.WASMB64, req.ManifestRaw)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	start := time.Now()
	results, err := s.sdk.Burn(r.Context(), artifact, req.Fn, req.Args)
	if err != nil {
		status := http.StatusInternalServerError
		if errors.Is(err, runtime.ErrNotImplemented) {
			status = http.StatusBadRequest
		}
		writeError(w, status, fmt.Sprintf("burn: %v", err))
		return
	}

	writeJSON(w, http.StatusOK, burnResponse{
		Results:   results,
		ElapsedMs: float64(time.Since(start).Microseconds()) / 1000.0,
	})
}

// batchItem is one element of a POST /batch request array.
type batchItem struct {
	ID          string          `json:"id"`
	WASMB64     string          `json:"wasm_b64"`
	ManifestRaw json.RawMessage `json:"manifest"`
	Fn          string          `json:"fn"`
	Args        []uint64        `json:"args"`
}

// batchResult is one element of a POST /batch response array.
type batchResult struct {
	ID        string   `json:"id"`
	Results   []uint64 `json:"results,omitempty"`
	Error     string   `json:"error,omitempty"`
	ElapsedMs float64  `json:"elapsed_ms"`
	OK        bool     `json:"ok"`
}

func (s *Server) handleBatch(w http.ResponseWriter, r *http.Request) {
	var items []batchItem
	if err := json.NewDecoder(r.Body).Decode(&items); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON: "+err.Error())
		return
	}

	jobs := make([]sdk.Job, 0, len(items))
	for i, item := range items {
		artifact, err := decodeArtifact(item.WASMB64, item.ManifestRaw)
		if err != nil {
			writeError(w, http.StatusBadRequest, fmt.Sprintf("item %d: %v", i, err))
			return
		}
		jobs = append(jobs, sdk.Job{
			ID:       item.ID,
			Artifact: artifact,
			Fn:       item.Fn,
			Args:     item.Args,
		})
	}

	sdkResults, err := s.sdk.Batch(r.Context(), jobs, 0)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "batch: "+err.Error())
		return
	}

	out := make([]batchResult, len(sdkResults))
	for i, res := range sdkResults {
		br := batchResult{
			ID:        res.ID,
			Results:   res.Values,
			ElapsedMs: float64(res.Elapsed.Microseconds()) / 1000.0,
			OK:        res.Err == nil,
		}
		if res.Err != nil {
			br.Error = res.Err.Error()
		}
		out[i] = br
	}
	writeJSON(w, http.StatusOK, out)
}

// --- Helpers ---

// decodeArtifact decodes a base64 WASM binary and a raw JSON manifest into an Artifact.
func decodeArtifact(wasmB64 string, manifestRaw json.RawMessage) (*sdk.Artifact, error) {
	wasmBytes, err := base64.StdEncoding.DecodeString(wasmB64)
	if err != nil {
		return nil, fmt.Errorf("invalid base64 wasm: %w", err)
	}

	// Unmarshal manifest from JSON.
	// We use the intent package's Manifest type directly via JSON round-trip.
	m, err := decodeManifest(manifestRaw)
	if err != nil {
		return nil, fmt.Errorf("invalid manifest JSON: %w", err)
	}

	return &sdk.Artifact{WASM: wasmBytes, Manifest: m}, nil
}

func writeJSON(w http.ResponseWriter, status int, v interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(v) //nolint:errcheck
}

func writeError(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]string{"error": msg})
}

// decodeManifest unmarshals a raw JSON manifest into an intent.Manifest.
func decodeManifest(raw json.RawMessage) (*intent.Manifest, error) {
	var m intent.Manifest
	if err := json.Unmarshal(raw, &m); err != nil {
		return nil, err
	}
	return &m, nil
}
