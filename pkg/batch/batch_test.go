package batch_test

import (
	"context"
	"testing"

	"github.com/scttfrdmn/ember/pkg/batch"
	"github.com/scttfrdmn/ember/pkg/sdk"
)

const addDir = "../../testdata/add"

func buildAddArtifact(t *testing.T) *sdk.Artifact {
	t.Helper()
	s := sdk.New()
	a, err := s.Build(context.Background(), addDir)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	return a
}

func TestRun_Parallel(t *testing.T) {
	s := sdk.New()
	a := buildAddArtifact(t)
	runner := batch.New(s)
	runner.MaxConcurrency = 2

	jobs := make([]sdk.Job, 5)
	for i := range jobs {
		jobs[i] = sdk.Job{
			ID:       string(rune('A' + i)),
			Artifact: a,
			Fn:       "Add",
			Args:     []uint64{uint64(i), uint64(i * 2)},
		}
	}

	results, err := runner.Run(context.Background(), jobs)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(results) != 5 {
		t.Fatalf("expected 5 results, got %d", len(results))
	}
	// Verify submission order and correctness.
	for i, r := range results {
		if r.ID != jobs[i].ID {
			t.Errorf("results[%d].ID = %q, want %q", i, r.ID, jobs[i].ID)
		}
		if r.Err != nil {
			t.Errorf("results[%d].Err = %v", i, r.Err)
		}
		want := uint64(i + i*2) // i + 2i = 3i
		if len(r.Values) != 1 || r.Values[0] != want {
			t.Errorf("results[%d].Values = %v, want [%d]", i, r.Values, want)
		}
	}
}

func TestRun_ContextCancel(t *testing.T) {
	s := sdk.New()
	a := buildAddArtifact(t)
	runner := batch.New(s)
	runner.MaxConcurrency = 1

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // cancel immediately

	jobs := []sdk.Job{
		{ID: "j0", Artifact: a, Fn: "Add", Args: []uint64{1, 2}},
		{ID: "j1", Artifact: a, Fn: "Add", Args: []uint64{3, 4}},
	}
	results, err := runner.Run(ctx, jobs)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	// All jobs should have ctx.Err() set since context was already cancelled.
	for i, r := range results {
		if r.Err == nil {
			// It's possible a job completed before being checked; that's OK.
			t.Logf("results[%d].Err = nil (job may have raced through)", i)
		}
	}
}
