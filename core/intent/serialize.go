package intent

import (
	"encoding/json"
	"fmt"
	"os"
)

// Marshal encodes the manifest to indented JSON bytes.
func Marshal(m *Manifest) ([]byte, error) {
	return json.MarshalIndent(m, "", "  ")
}

// Unmarshal decodes a manifest from JSON bytes.
func Unmarshal(data []byte) (*Manifest, error) {
	var m Manifest
	if err := json.Unmarshal(data, &m); err != nil {
		return nil, fmt.Errorf("unmarshal manifest: %w", err)
	}
	return &m, nil
}

// WriteFile writes the manifest as indented JSON to the named file,
// creating or truncating it.
func WriteFile(path string, m *Manifest) error {
	data, err := Marshal(m)
	if err != nil {
		return err
	}
	data = append(data, '\n')
	return os.WriteFile(path, data, 0o644)
}

// ReadFile reads and decodes a manifest from a JSON file.
func ReadFile(path string) (*Manifest, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read manifest %s: %w", path, err)
	}
	return Unmarshal(data)
}
