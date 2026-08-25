package management

import (
	"encoding/json"
	"testing"
)

func TestParseQoderModelDefaultsBody_WrappedPayload(t *testing.T) {
	entries, err := parseQoderModelDefaultsBody([]byte(`{"qoder-model-defaults":{"dfmodel":{"context":"1M"}}}`))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if entries["dfmodel"].Context != "1M" {
		t.Fatalf("entries = %#v", entries)
	}
	if _, ok := entries["qoder-model-defaults"]; ok {
		t.Fatal("wrapper key should not become a model entry")
	}
}

func TestParseQoderModelDefaultsBody_RawMap(t *testing.T) {
	entries, err := parseQoderModelDefaultsBody([]byte(`{"dmodel":{"thinking":"max","context":"400K"}}`))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if entries["dmodel"].Thinking != "max" || entries["dmodel"].Context != "400K" {
		t.Fatalf("entries = %#v", entries)
	}
}

func TestParseQoderModelDefaultsBody_EmptyWrapperClears(t *testing.T) {
	entries, err := parseQoderModelDefaultsBody([]byte(`{"qoder-model-defaults":{}}`))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if len(entries) != 0 {
		t.Fatalf("entries = %#v", entries)
	}
}

func TestParseQoderModelDefaultsBody_OldUnmarshalWouldDropWrapper(t *testing.T) {
	raw := []byte(`{"qoder-model-defaults":{"dfmodel":{"context":"1M"}}}`)
	var entries map[string]struct {
		Thinking string `json:"thinking"`
		Context  string `json:"context"`
	}
	if err := json.Unmarshal(raw, &entries); err != nil {
		t.Fatalf("legacy unmarshal should succeed: %v", err)
	}
	if entries["qoder-model-defaults"].Context != "" {
		t.Fatal("expected the naive map unmarshal to ignore nested context")
	}
	parsed, err := parseQoderModelDefaultsBody(raw)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if parsed["dfmodel"].Context != "1M" {
		t.Fatalf("wrapper parse = %#v", parsed)
	}
}
