package serve_test

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/scttfrdmn/ember/pkg/sdk"
	"github.com/scttfrdmn/ember/pkg/serve"
)

const addDir = "../../testdata/add"

func newTestServer(t *testing.T) *httptest.Server {
	t.Helper()
	s := sdk.New()
	srv := serve.New(s)
	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(ts.Close)
	return ts
}

func TestHealth(t *testing.T) {
	ts := newTestServer(t)

	resp, err := http.Get(ts.URL + "/health")
	if err != nil {
		t.Fatalf("GET /health: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Errorf("status = %d, want 200", resp.StatusCode)
	}
	var body map[string]string
	json.NewDecoder(resp.Body).Decode(&body)
	if body["status"] != "ok" {
		t.Errorf("body status = %q, want %q", body["status"], "ok")
	}
	if body["version"] == "" {
		t.Error("expected non-empty version")
	}
}

func TestCapabilities(t *testing.T) {
	ts := newTestServer(t)

	resp, err := http.Get(ts.URL + "/capabilities")
	if err != nil {
		t.Fatalf("GET /capabilities: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Errorf("status = %d, want 200", resp.StatusCode)
	}
	var caps map[string]interface{}
	json.NewDecoder(resp.Body).Decode(&caps)
	if _, ok := caps["max_memory_pages"]; !ok {
		t.Error("expected max_memory_pages in capabilities")
	}
	if _, ok := caps["cores"]; !ok {
		t.Error("expected cores in capabilities")
	}
}

func TestBuildAndBurn(t *testing.T) {
	ts := newTestServer(t)

	const addSource = `package add

func Add(a, b int) int { return a + b }
`
	buildReq := map[string]string{
		"source_b64": base64.StdEncoding.EncodeToString([]byte(addSource)),
		"filename":   "add.go",
	}
	buildBody, _ := json.Marshal(buildReq)

	resp, err := http.Post(ts.URL+"/build", "application/json", bytes.NewReader(buildBody))
	if err != nil {
		t.Fatalf("POST /build: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("POST /build status = %d: %s", resp.StatusCode, body)
	}

	var buildResp map[string]interface{}
	json.NewDecoder(resp.Body).Decode(&buildResp)
	wasmB64, ok := buildResp["wasm_b64"].(string)
	if !ok || wasmB64 == "" {
		t.Fatal("expected non-empty wasm_b64 in build response")
	}
	manifestRaw, _ := json.Marshal(buildResp["manifest"])

	burnReq := map[string]interface{}{
		"wasm_b64": wasmB64,
		"manifest": json.RawMessage(manifestRaw),
		"fn":       "Add",
		"args":     []uint64{3, 4},
	}
	burnBody, _ := json.Marshal(burnReq)

	resp2, err := http.Post(ts.URL+"/burn", "application/json", bytes.NewReader(burnBody))
	if err != nil {
		t.Fatalf("POST /burn: %v", err)
	}
	defer resp2.Body.Close()
	if resp2.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp2.Body)
		t.Fatalf("POST /burn status = %d: %s", resp2.StatusCode, body)
	}

	var burnResp map[string]interface{}
	json.NewDecoder(resp2.Body).Decode(&burnResp)
	results, ok := burnResp["results"].([]interface{})
	if !ok || len(results) == 0 {
		t.Fatalf("expected results in burn response, got %v", burnResp)
	}
}

func TestBatch(t *testing.T) {
	ts := newTestServer(t)
	s := sdk.New()

	a, err := s.Build(context.Background(), addDir)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	manifestJSON, _ := json.Marshal(a.Manifest)

	type batchItem struct {
		ID       string          `json:"id"`
		WASMB64  string          `json:"wasm_b64"`
		Manifest json.RawMessage `json:"manifest"`
		Fn       string          `json:"fn"`
		Args     []uint64        `json:"args"`
	}
	items := []batchItem{
		{ID: "j0", WASMB64: base64.StdEncoding.EncodeToString(a.WASM), Manifest: manifestJSON, Fn: "Add", Args: []uint64{1, 2}},
		{ID: "j1", WASMB64: base64.StdEncoding.EncodeToString(a.WASM), Manifest: manifestJSON, Fn: "Add", Args: []uint64{10, 20}},
	}
	reqBody, _ := json.Marshal(items)

	resp, err := http.Post(ts.URL+"/batch", "application/json", bytes.NewReader(reqBody))
	if err != nil {
		t.Fatalf("POST /batch: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("POST /batch status = %d: %s", resp.StatusCode, body)
	}

	var batchResp []map[string]interface{}
	json.NewDecoder(resp.Body).Decode(&batchResp)
	if len(batchResp) != 2 {
		t.Fatalf("expected 2 batch results, got %d", len(batchResp))
	}
	for _, r := range batchResp {
		if okVal, _ := r["ok"].(bool); !okVal {
			t.Errorf("batch result not ok: %v", r)
		}
	}
}
