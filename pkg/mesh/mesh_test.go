package mesh_test

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/scttfrdmn/ember/core/intent"
	"github.com/scttfrdmn/ember/pkg/hearth"
	"github.com/scttfrdmn/ember/pkg/mesh"
	"github.com/scttfrdmn/ember/pkg/sdk"
)

const addDir = "../../testdata/add"

func buildArtifact(t *testing.T) *sdk.Artifact {
	t.Helper()
	s := sdk.New()
	a, err := s.Build(context.Background(), addDir)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	return a
}

// mockCapabilitiesServer returns a test server that serves a capabilities JSON response.
func mockCapabilitiesServer(t *testing.T, caps hearth.Capabilities) *httptest.Server {
	t.Helper()
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/capabilities" {
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(caps)
			return
		}
		http.NotFound(w, r)
	}))
	t.Cleanup(ts.Close)
	return ts
}

func TestAddNode(t *testing.T) {
	caps := hearth.New().Capabilities()
	ts := mockCapabilitiesServer(t, caps)

	m := mesh.New()
	if err := m.AddNode(context.Background(), ts.URL); err != nil {
		t.Fatalf("AddNode: %v", err)
	}
	nodes := m.Nodes()
	if len(nodes) != 1 {
		t.Fatalf("expected 1 node, got %d", len(nodes))
	}
	if !nodes[0].Healthy {
		t.Error("expected node to be healthy")
	}
	if nodes[0].Addr != ts.URL {
		t.Errorf("Addr = %q, want %q", nodes[0].Addr, ts.URL)
	}
}

func TestRoute_NoCapableNode(t *testing.T) {
	m := mesh.New()
	// No nodes registered.
	manifest := &intent.Manifest{}
	_, err := m.Route(manifest)
	if err != mesh.ErrNoCapableNode {
		t.Errorf("Route with no nodes: err = %v, want ErrNoCapableNode", err)
	}
}

func TestRoute_CapableNode(t *testing.T) {
	caps := hearth.New().Capabilities()
	ts := mockCapabilitiesServer(t, caps)

	m := mesh.New()
	if err := m.AddNode(context.Background(), ts.URL); err != nil {
		t.Fatalf("AddNode: %v", err)
	}

	manifest := &intent.Manifest{}
	node, err := m.Route(manifest)
	if err != nil {
		t.Fatalf("Route: %v", err)
	}
	if node.Addr != ts.URL {
		t.Errorf("Route returned addr = %q, want %q", node.Addr, ts.URL)
	}
}

func TestBurn_Remote(t *testing.T) {
	a := buildArtifact(t)
	manifestJSON, _ := json.Marshal(a.Manifest)

	// Mock remote /burn endpoint.
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/capabilities":
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(hearth.New().Capabilities())
		case "/burn":
			var req struct {
				WASMB64     string          `json:"wasm_b64"`
				ManifestRaw json.RawMessage `json:"manifest"`
				Fn          string          `json:"fn"`
				Args        []float64       `json:"args"`
			}
			json.NewDecoder(r.Body).Decode(&req)
			// Decode and execute locally.
			wasmBytes, _ := base64.StdEncoding.DecodeString(req.WASMB64)
			var m intent.Manifest
			json.Unmarshal(req.ManifestRaw, &m)
			args := make([]uint64, len(req.Args))
			for i, v := range req.Args {
				args[i] = uint64(v)
			}
			h := hearth.New()
			results, err := h.Burn(r.Context(), wasmBytes, &m, req.Fn, args)
			if err != nil {
				http.Error(w, `{"error":"`+err.Error()+`"}`, http.StatusInternalServerError)
				return
			}
			json.NewEncoder(w).Encode(map[string]interface{}{"results": results})
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(ts.Close)

	_ = manifestJSON

	m := mesh.New()
	if err := m.AddNode(context.Background(), ts.URL); err != nil {
		t.Fatalf("AddNode: %v", err)
	}

	results, err := m.Burn(context.Background(), a.WASM, a.Manifest, "Add", []uint64{3, 4})
	if err != nil {
		t.Fatalf("Burn: %v", err)
	}
	if len(results) != 1 || results[0] != 7 {
		t.Errorf("Burn(3,4) = %v, want [7]", results)
	}
}

func TestBurn_LocalFallback(t *testing.T) {
	a := buildArtifact(t)

	// No remote nodes — should fall back to local hearth.
	m := mesh.New()
	results, err := m.Burn(context.Background(), a.WASM, a.Manifest, "Add", []uint64{5, 6})
	if err != nil {
		t.Fatalf("Burn (local fallback): %v", err)
	}
	if len(results) != 1 || results[0] != 11 {
		t.Errorf("Burn(5,6) = %v, want [11]", results)
	}
}

func TestRemoveNode(t *testing.T) {
	caps := hearth.New().Capabilities()
	ts := mockCapabilitiesServer(t, caps)

	m := mesh.New()
	if err := m.AddNode(context.Background(), ts.URL); err != nil {
		t.Fatalf("AddNode: %v", err)
	}
	if len(m.Nodes()) != 1 {
		t.Fatal("expected 1 node after AddNode")
	}
	m.RemoveNode(ts.URL)
	if len(m.Nodes()) != 0 {
		t.Errorf("expected 0 nodes after RemoveNode, got %d", len(m.Nodes()))
	}
}
