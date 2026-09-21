package config

import (
	"testing"
	"time"

	"gopkg.in/yaml.v3"
)

func TestFormatYAMLDuration(t *testing.T) {
	t.Parallel()

	tests := []struct {
		in   time.Duration
		want string
	}{
		{0, "0s"},
		{12 * time.Hour, "12h"},
		{45 * time.Second, "45s"},
		{10 * time.Minute, "10m"},
		{90 * time.Second, "90s"},
		{1500 * time.Millisecond, "1.5s"},
	}
	for _, tc := range tests {
		if got := formatYAMLDuration(tc.in); got != tc.want {
			t.Fatalf("formatYAMLDuration(%s) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestParseYAMLDurationNode(t *testing.T) {
	t.Parallel()

	tests := []struct {
		value string
		tag   string
		want  time.Duration
	}{
		{value: "12h", want: 12 * time.Hour},
		{value: "45s", want: 45 * time.Second},
		{value: "43200000000000", tag: "!!int", want: 12 * time.Hour},
	}
	for _, tc := range tests {
		node := &yaml.Node{Kind: yaml.ScalarNode, Tag: tc.tag, Value: tc.value}
		got, ok, err := parseYAMLDurationNode(node)
		if err != nil || !ok {
			t.Fatalf("parseYAMLDurationNode(%q) error=%v ok=%v", tc.value, err, ok)
		}
		if got != tc.want {
			t.Fatalf("parseYAMLDurationNode(%q) = %s, want %s", tc.value, got, tc.want)
		}
	}
}
