// Package toolspec converts ember Artifacts to LLM function-calling tool specs.
// Supports OpenAI function-calling format and Anthropic Claude tool-use format.
package toolspec

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/scttfrdmn/ember/pkg/sdk"
)

// OpenAITool is the OpenAI function-calling tool format.
// See: https://platform.openai.com/docs/api-reference/chat/create#tools
type OpenAITool struct {
	Type     string     `json:"type"`     // always "function"
	Function OpenAIFunc `json:"function"`
}

// OpenAIFunc is the function definition within an OpenAITool.
type OpenAIFunc struct {
	Name        string    `json:"name"`
	Description string    `json:"description"`
	Parameters  ParamSpec `json:"parameters"`
}

// ParamSpec is a JSON Schema "object" describing the function's parameters.
type ParamSpec struct {
	Type       string               `json:"type"` // "object"
	Properties map[string]ParamProp `json:"properties"`
	Required   []string             `json:"required"`
}

// ParamProp is a single JSON Schema property (type only for now).
type ParamProp struct {
	Type string `json:"type"` // "integer", "number", or "boolean"
}

// AnthropicTool is the Anthropic Claude tool-use format.
// See: https://docs.anthropic.com/en/api/messages
type AnthropicTool struct {
	Name        string    `json:"name"`
	Description string    `json:"description"`
	InputSchema ParamSpec `json:"input_schema"`
}

// FromArtifact generates one OpenAITool per exported function in the artifact.
// Returns an error if the artifact has no exports.
func FromArtifact(a *sdk.Artifact) ([]OpenAITool, error) {
	if len(a.Exports) == 0 {
		return nil, fmt.Errorf("toolspec: artifact has no exported functions")
	}

	var tools []OpenAITool
	for _, exp := range a.Exports {
		tool, err := fromExportSig(exp, a)
		if err != nil {
			return nil, fmt.Errorf("toolspec: function %s: %w", exp.Name, err)
		}
		tools = append(tools, tool)
	}
	return tools, nil
}

// ToAnthropic converts a slice of OpenAI tools to Anthropic Claude tool-use format.
// The parameter schema is identical; only the top-level key differs (parameters →
// input_schema) and the "type": "function" wrapper is dropped.
func ToAnthropic(tools []OpenAITool) []AnthropicTool {
	out := make([]AnthropicTool, len(tools))
	for i, t := range tools {
		out[i] = AnthropicTool{
			Name:        t.Function.Name,
			Description: t.Function.Description,
			InputSchema: t.Function.Parameters,
		}
	}
	return out
}

// MarshalJSON returns pretty-printed JSON for a slice of OpenAI tools.
func MarshalJSON(tools []OpenAITool) ([]byte, error) {
	return json.MarshalIndent(tools, "", "  ")
}

// fromExportSig builds one OpenAITool from an ExportSig and the artifact's manifest.
func fromExportSig(exp sdk.ExportSig, a *sdk.Artifact) (OpenAITool, error) {
	props := make(map[string]ParamProp, len(exp.Params))
	required := make([]string, 0, len(exp.Params))

	for i, pt := range exp.Params {
		name := paramName(exp.ParamNames, i)
		jsonType, err := jsonSchemaType(pt)
		if err != nil {
			return OpenAITool{}, err
		}
		props[name] = ParamProp{Type: jsonType}
		required = append(required, name)
	}

	desc := buildDescription(a)

	return OpenAITool{
		Type: "function",
		Function: OpenAIFunc{
			Name:        exp.Name,
			Description: desc,
			Parameters: ParamSpec{
				Type:       "object",
				Properties: props,
				Required:   required,
			},
		},
	}, nil
}

// buildDescription generates a description from the manifest's runtime strips.
func buildDescription(a *sdk.Artifact) string {
	if a.Manifest == nil || len(a.Manifest.RuntimeStrips) == 0 {
		return "Pure compute ember function."
	}
	return fmt.Sprintf("Pure compute ember function. Strips: %s.",
		strings.Join(a.Manifest.RuntimeStrips, ", "))
}

// paramName returns the parameter name at index i, falling back to "p<i>" if
// the name is missing, blank, or the placeholder "_".
func paramName(names []string, i int) string {
	if i < len(names) && names[i] != "" && names[i] != "_" {
		return names[i]
	}
	return fmt.Sprintf("p%d", i)
}

// jsonSchemaType maps a ParamType to a JSON Schema type string.
func jsonSchemaType(pt sdk.ParamType) (string, error) {
	switch pt {
	case sdk.ParamTypeInt:
		return "integer", nil
	case sdk.ParamTypeFloat32, sdk.ParamTypeFloat64:
		return "number", nil
	case sdk.ParamTypeBool:
		return "boolean", nil
	default:
		return "", fmt.Errorf("unsupported ParamType %d", pt)
	}
}
