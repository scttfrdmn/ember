package sdk_test

import (
	"context"
	"testing"

	"github.com/scttfrdmn/ember/pkg/sdk"
)

const addDir = "../../testdata/add"

func TestBuild_Add(t *testing.T) {
	s := sdk.New()
	a, err := s.Build(context.Background(), addDir)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	if len(a.WASM) == 0 {
		t.Error("expected non-empty WASM bytes")
	}
	if a.Manifest == nil {
		t.Error("expected non-nil Manifest")
	}
	if len(a.Exports) == 0 {
		t.Error("expected at least one export")
	}
}

func TestExportSig_Add(t *testing.T) {
	s := sdk.New()
	a, err := s.Build(context.Background(), addDir)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	if len(a.Exports) != 1 {
		t.Fatalf("expected 1 export, got %d", len(a.Exports))
	}
	sig := a.Exports[0]
	if sig.Name != "Add" {
		t.Errorf("Name = %q, want %q", sig.Name, "Add")
	}
	if len(sig.Params) != 2 {
		t.Errorf("Params len = %d, want 2", len(sig.Params))
	}
	for i, p := range sig.Params {
		if p != sdk.ParamTypeInt {
			t.Errorf("Params[%d] = %v, want ParamTypeInt", i, p)
		}
	}
	if len(sig.Results) != 1 || sig.Results[0] != sdk.ParamTypeInt {
		t.Errorf("Results = %v, want [ParamTypeInt]", sig.Results)
	}
	if len(sig.ParamNames) != 2 {
		t.Errorf("ParamNames len = %d, want 2", len(sig.ParamNames))
	}
	if sig.ParamNames[0] != "a" || sig.ParamNames[1] != "b" {
		t.Errorf("ParamNames = %v, want [a b]", sig.ParamNames)
	}
}

func TestBurn_Add(t *testing.T) {
	s := sdk.New()
	a, err := s.Build(context.Background(), addDir)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	results, err := s.Burn(context.Background(), a, "Add", []uint64{3, 4})
	if err != nil {
		t.Fatalf("Burn: %v", err)
	}
	if len(results) != 1 || results[0] != 7 {
		t.Errorf("Burn Add(3,4) = %v, want [7]", results)
	}
}

func TestBatch_Multiple(t *testing.T) {
	s := sdk.New()
	a, err := s.Build(context.Background(), addDir)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}

	jobs := []sdk.Job{
		{ID: "j0", Artifact: a, Fn: "Add", Args: []uint64{1, 2}},
		{ID: "j1", Artifact: a, Fn: "Add", Args: []uint64{10, 20}},
		{ID: "j2", Artifact: a, Fn: "Add", Args: []uint64{100, 200}},
	}
	results, err := s.Batch(context.Background(), jobs, 0)
	if err != nil {
		t.Fatalf("Batch: %v", err)
	}
	if len(results) != 3 {
		t.Fatalf("Batch: expected 3 results, got %d", len(results))
	}
	// Verify submission order and correctness.
	want := []uint64{3, 30, 300}
	for i, r := range results {
		if r.ID != jobs[i].ID {
			t.Errorf("results[%d].ID = %q, want %q", i, r.ID, jobs[i].ID)
		}
		if r.Err != nil {
			t.Errorf("results[%d].Err = %v", i, r.Err)
		}
		if len(r.Values) != 1 || r.Values[0] != want[i] {
			t.Errorf("results[%d].Values = %v, want [%d]", i, r.Values, want[i])
		}
	}
}
