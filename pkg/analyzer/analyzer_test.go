package analyzer

import (
	"testing"

	"github.com/scttfrdmn/ember/core/intent"
	"github.com/scttfrdmn/ember/core/ssa/loader"
	"github.com/scttfrdmn/ember/core/ssa/walker"
)

func TestAnalyzer_Add(t *testing.T) {
	lp, err := loader.LoadDir("../../testdata/add")
	if err != nil {
		t.Fatalf("LoadDir: %v", err)
	}

	a := New()
	w := walker.New(a)
	if err := w.WalkPackage(lp.MainPkg); err != nil {
		t.Fatalf("WalkPackage: %v", err)
	}
	m := a.Manifest()

	// Pure arithmetic: all these must be false.
	if m.HasGoroutines {
		t.Error("HasGoroutines should be false for pure arithmetic")
	}
	if m.HasGC {
		t.Error("HasGC should be false for pure arithmetic")
	}
	if m.HasChannels {
		t.Error("HasChannels should be false for pure arithmetic")
	}
	if m.HasDefer {
		t.Error("HasDefer should be false for pure arithmetic")
	}
	if m.HasPanic {
		t.Error("HasPanic should be false for pure arithmetic")
	}
	if m.HasReflection {
		t.Error("HasReflection should be false for pure arithmetic")
	}
	if m.MaxMemoryBytes != 0 {
		t.Errorf("MaxMemoryBytes = %d, want 0", m.MaxMemoryBytes)
	}
	if len(m.Capabilities) != 0 {
		t.Errorf("Capabilities = %v, want empty", m.Capabilities)
	}

	// InstructionCount: Add has at least BinOp + Return = 2.
	if m.InstructionCount < 2 {
		t.Errorf("InstructionCount = %d, want >= 2", m.InstructionCount)
	}

	// RuntimeStrips should contain the standard pure-compute set.
	wantStrips := []string{
		string(intent.StripChannels),
		string(intent.StripDefer),
		string(intent.StripGC),
		string(intent.StripReflection),
		string(intent.StripScheduler),
	}
	if len(m.RuntimeStrips) != len(wantStrips) {
		t.Errorf("RuntimeStrips = %v, want %v", m.RuntimeStrips, wantStrips)
	} else {
		for i, s := range wantStrips {
			if m.RuntimeStrips[i] != s {
				t.Errorf("RuntimeStrips[%d] = %q, want %q", i, m.RuntimeStrips[i], s)
			}
		}
	}

	// IsPureCompute should be true.
	if !m.IsPureCompute() {
		t.Error("Add should be IsPureCompute")
	}
}
