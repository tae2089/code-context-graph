package parser

import "testing"

func TestParseManifest(t *testing.T) {
	data := []byte(`{
		"key":"ccg-parser-swift",
		"api_version":"1.0",
		"entry":"./ccg-parser-swift",
		"extensions":[".swift"],
		"languages":["swift"],
		"capabilities":["nodes","edges"]
	}`)

	m, err := parseManifest(data)
	if err != nil {
		t.Fatalf("parseManifest() error = %v", err)
	}
	if m.Key != "ccg-parser-swift" {
		t.Errorf("Key = %q, want %q", m.Key, "ccg-parser-swift")
	}
	if m.APIVersion != "1.0" {
		t.Errorf("APIVersion = %q, want %q", m.APIVersion, "1.0")
	}
	if len(m.Extensions) != 1 || m.Extensions[0] != ".swift" {
		t.Errorf("Extensions = %v, want [.swift]", m.Extensions)
	}
	if len(m.Capabilities) != 2 {
		t.Errorf("Capabilities = %v, want 2 entries", m.Capabilities)
	}
}

func TestParseManifestInvalidJSON(t *testing.T) {
	if _, err := parseManifest([]byte("{not json")); err == nil {
		t.Error("parseManifest() on invalid JSON = nil error, want error")
	}
}

func TestAPIMajorCompatible(t *testing.T) {
	cases := []struct {
		plugin, core string
		want         bool
	}{
		{"1.0", "1.0", true},
		{"1.5", "1.0", true}, // same major, different minor
		{"1.0", "1.9", true},
		{"2.0", "1.0", false}, // different major
		{"1.0", "2.0", false},
	}
	for _, c := range cases {
		if got := apiMajorCompatible(c.plugin, c.core); got != c.want {
			t.Errorf("apiMajorCompatible(%q,%q) = %v, want %v", c.plugin, c.core, got, c.want)
		}
	}
}
