package toolspec_test

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/scttfrdmn/ember/pkg/sdk"
	"github.com/scttfrdmn/ember/pkg/toolspec"
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

func TestFromArtifact_Add(t *testing.T) {
	a := buildAddArtifact(t)
	tools, err := toolspec.FromArtifact(a)
	if err != nil {
		t.Fatalf("FromArtifact: %v", err)
	}
	if len(tools) != 1 {
		t.Fatalf("expected 1 tool, got %d", len(tools))
	}
	tool := tools[0]
	if tool.Type != "function" {
		t.Errorf("Type = %q, want %q", tool.Type, "function")
	}
	if tool.Function.Name != "Add" {
		t.Errorf("Function.Name = %q, want %q", tool.Function.Name, "Add")
	}
	if tool.Function.Description == "" {
		t.Error("expected non-empty description")
	}

	params := tool.Function.Parameters
	if params.Type != "object" {
		t.Errorf("Parameters.Type = %q, want %q", params.Type, "object")
	}
	if len(params.Properties) != 2 {
		t.Errorf("Properties len = %d, want 2", len(params.Properties))
	}
	for _, name := range []string{"a", "b"} {
		prop, ok := params.Properties[name]
		if !ok {
			t.Errorf("Properties missing %q", name)
			continue
		}
		if prop.Type != "integer" {
			t.Errorf("Properties[%q].Type = %q, want %q", name, prop.Type, "integer")
		}
	}
	if len(params.Required) != 2 {
		t.Errorf("Required len = %d, want 2", len(params.Required))
	}
}

func TestFromArtifact_JSONShape(t *testing.T) {
	a := buildAddArtifact(t)
	tools, err := toolspec.FromArtifact(a)
	if err != nil {
		t.Fatalf("FromArtifact: %v", err)
	}
	b, err := toolspec.MarshalJSON(tools)
	if err != nil {
		t.Fatalf("MarshalJSON: %v", err)
	}
	// Verify it's valid JSON.
	var raw []interface{}
	if err := json.Unmarshal(b, &raw); err != nil {
		t.Fatalf("MarshalJSON output is not valid JSON: %v", err)
	}
	if len(raw) != 1 {
		t.Errorf("expected 1 tool in JSON, got %d", len(raw))
	}
}

func TestToAnthropic(t *testing.T) {
	a := buildAddArtifact(t)
	tools, err := toolspec.FromArtifact(a)
	if err != nil {
		t.Fatalf("FromArtifact: %v", err)
	}
	anthropic := toolspec.ToAnthropic(tools)
	if len(anthropic) != len(tools) {
		t.Fatalf("ToAnthropic: expected %d tools, got %d", len(tools), len(anthropic))
	}
	at := anthropic[0]
	if at.Name != tools[0].Function.Name {
		t.Errorf("Name = %q, want %q", at.Name, tools[0].Function.Name)
	}
	if at.Description != tools[0].Function.Description {
		t.Errorf("Description mismatch")
	}
	// Verify JSON: input_schema key, no "type": "function" wrapper.
	b, _ := json.Marshal(at)
	var m map[string]interface{}
	json.Unmarshal(b, &m)
	if _, ok := m["input_schema"]; !ok {
		t.Error("expected 'input_schema' key in Anthropic tool JSON")
	}
	if _, ok := m["type"]; ok {
		t.Error("Anthropic tool should not have 'type' key")
	}
}
