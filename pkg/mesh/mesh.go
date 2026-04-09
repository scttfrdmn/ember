// Package mesh implements a distributed hearth mesh with capability routing.
// Nodes are discovered by probing their /capabilities endpoint. Burns are
// routed to capable nodes via round-robin; falls back to the local hearth
// when no remote node can satisfy the manifest.
package mesh

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"sync"
	"sync/atomic"

	"github.com/scttfrdmn/ember/core/intent"
	"github.com/scttfrdmn/ember/pkg/hearth"
)

// ErrNoCapableNode is returned when no healthy node in the mesh can satisfy
// the ember's intent manifest.
var ErrNoCapableNode = errors.New("mesh: no capable node found for manifest")

// NodeInfo holds the address, capabilities, and health state of one mesh node.
type NodeInfo struct {
	Addr    string
	Caps    hearth.Capabilities
	Healthy bool
}

// Mesh is a registry of remote hearth nodes with local fallback.
// The zero value is not useful; create one with New.
type Mesh struct {
	mu         sync.RWMutex
	nodes      []NodeInfo
	local      *hearth.Hearth
	roundRobin atomic.Uint64
}

// New creates a Mesh backed by a local hearth (auto-detected capabilities).
func New() *Mesh {
	return &Mesh{local: hearth.New()}
}

// AddNode probes addr/capabilities, reads the node's capability fingerprint,
// and adds it to the mesh as a healthy node. Returns an error if the probe
// fails or returns invalid JSON.
func (m *Mesh) AddNode(ctx context.Context, addr string) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, addr+"/capabilities", nil)
	if err != nil {
		return fmt.Errorf("mesh.AddNode(%s): build request: %w", addr, err)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return fmt.Errorf("mesh.AddNode(%s): probe: %w", addr, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("mesh.AddNode(%s): probe returned HTTP %d", addr, resp.StatusCode)
	}

	var caps hearth.Capabilities
	if err := json.NewDecoder(resp.Body).Decode(&caps); err != nil {
		return fmt.Errorf("mesh.AddNode(%s): decode capabilities: %w", addr, err)
	}

	m.mu.Lock()
	m.nodes = append(m.nodes, NodeInfo{Addr: addr, Caps: caps, Healthy: true})
	m.mu.Unlock()
	return nil
}

// RemoveNode removes all nodes with the given address from the mesh.
func (m *Mesh) RemoveNode(addr string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := m.nodes[:0]
	for _, n := range m.nodes {
		if n.Addr != addr {
			out = append(out, n)
		}
	}
	m.nodes = out
}

// Nodes returns a snapshot of all currently registered nodes.
func (m *Mesh) Nodes() []NodeInfo {
	m.mu.RLock()
	defer m.mu.RUnlock()
	snap := make([]NodeInfo, len(m.nodes))
	copy(snap, m.nodes)
	return snap
}

// Route picks a capable, healthy remote node for the given manifest using
// round-robin among candidates. Returns ErrNoCapableNode if none qualify.
func (m *Mesh) Route(manifest *intent.Manifest) (*NodeInfo, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	var candidates []int
	for i, n := range m.nodes {
		if n.Healthy && hearth.NewWithCaps(n.Caps).CanBurn(manifest) {
			candidates = append(candidates, i)
		}
	}
	if len(candidates) == 0 {
		return nil, ErrNoCapableNode
	}
	idx := int(m.roundRobin.Add(1)-1) % len(candidates)
	node := m.nodes[candidates[idx]]
	return &node, nil
}

// Burn routes execution to a capable remote node, falling back to the local
// hearth if no remote node is available. Only returns an error when both
// remote routing and local execution fail.
func (m *Mesh) Burn(ctx context.Context, wasmBytes []byte, manifest *intent.Manifest, fn string, args []uint64) ([]uint64, error) {
	node, err := m.Route(manifest)
	if errors.Is(err, ErrNoCapableNode) {
		// Fall back to local hearth.
		return m.local.Burn(ctx, wasmBytes, manifest, fn, args)
	}
	if err != nil {
		return nil, err
	}
	return m.burnRemote(ctx, node.Addr, wasmBytes, manifest, fn, args)
}

// burnRemote sends a POST /burn request to a remote hearth node.
func (m *Mesh) burnRemote(ctx context.Context, addr string, wasmBytes []byte, manifest *intent.Manifest, fn string, args []uint64) ([]uint64, error) {
	manifestJSON, err := json.Marshal(manifest)
	if err != nil {
		return nil, fmt.Errorf("mesh.burnRemote: marshal manifest: %w", err)
	}

	body := struct {
		WASMB64     string          `json:"wasm_b64"`
		ManifestRaw json.RawMessage `json:"manifest"`
		Fn          string          `json:"fn"`
		Args        []uint64        `json:"args"`
	}{
		WASMB64:     base64.StdEncoding.EncodeToString(wasmBytes),
		ManifestRaw: manifestJSON,
		Fn:          fn,
		Args:        args,
	}

	bodyJSON, err := json.Marshal(body)
	if err != nil {
		return nil, fmt.Errorf("mesh.burnRemote: marshal request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, addr+"/burn", bytes.NewReader(bodyJSON))
	if err != nil {
		return nil, fmt.Errorf("mesh.burnRemote: build request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("mesh.burnRemote(%s): %w", addr, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		var errResp struct {
			Error string `json:"error"`
		}
		json.NewDecoder(resp.Body).Decode(&errResp) //nolint:errcheck
		return nil, fmt.Errorf("mesh.burnRemote(%s): HTTP %d: %s", addr, resp.StatusCode, errResp.Error)
	}

	var result struct {
		Results []uint64 `json:"results"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("mesh.burnRemote(%s): decode response: %w", addr, err)
	}
	return result.Results, nil
}
