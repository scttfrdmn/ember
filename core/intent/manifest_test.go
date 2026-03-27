package intent

import (
	"testing"
)

func TestComputeRuntimeStrips_AllFalse(t *testing.T) {
	m := &Manifest{}
	m.ComputeRuntimeStrips()
	want := []string{"channels", "defer", "gc", "reflection", "scheduler"}
	if len(m.RuntimeStrips) != len(want) {
		t.Fatalf("got %v, want %v", m.RuntimeStrips, want)
	}
	for i, s := range want {
		if m.RuntimeStrips[i] != s {
			t.Errorf("RuntimeStrips[%d] = %q, want %q", i, m.RuntimeStrips[i], s)
		}
	}
}

func TestComputeRuntimeStrips_HasGoroutines(t *testing.T) {
	m := &Manifest{HasGoroutines: true}
	m.ComputeRuntimeStrips()
	for _, s := range m.RuntimeStrips {
		if s == string(StripScheduler) {
			t.Error("scheduler should not be stripped when HasGoroutines=true")
		}
	}
}

func TestComputeRuntimeStrips_Sorted(t *testing.T) {
	m := &Manifest{}
	m.ComputeRuntimeStrips()
	for i := 1; i < len(m.RuntimeStrips); i++ {
		if m.RuntimeStrips[i] < m.RuntimeStrips[i-1] {
			t.Errorf("RuntimeStrips not sorted: %v", m.RuntimeStrips)
		}
	}
}

func TestMarshalRoundTrip(t *testing.T) {
	m := &Manifest{
		MaxMemoryBytes:   0,
		HasGoroutines:    false,
		HasGC:            false,
		HasChannels:      false,
		HasDefer:         false,
		HasPanic:         false,
		HasReflection:    false,
		InstructionCount: 2,
	}
	m.ComputeRuntimeStrips()

	data, err := Marshal(m)
	if err != nil {
		t.Fatal(err)
	}
	got, err := Unmarshal(data)
	if err != nil {
		t.Fatal(err)
	}
	if got.InstructionCount != m.InstructionCount {
		t.Errorf("InstructionCount: got %d, want %d", got.InstructionCount, m.InstructionCount)
	}
	if len(got.RuntimeStrips) != len(m.RuntimeStrips) {
		t.Errorf("RuntimeStrips: got %v, want %v", got.RuntimeStrips, m.RuntimeStrips)
	}
}

func TestIsPureCompute(t *testing.T) {
	m := &Manifest{}
	if !m.IsPureCompute() {
		t.Error("zero-value Manifest should be pure compute")
	}
	m.HasGoroutines = true
	if m.IsPureCompute() {
		t.Error("Manifest with goroutines should not be pure compute")
	}
}
