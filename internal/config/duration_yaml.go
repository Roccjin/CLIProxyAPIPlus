package config

import (
	"fmt"
	"strconv"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

func formatYAMLDuration(d time.Duration) string {
	if d == 0 {
		return "0s"
	}
	if d%time.Hour == 0 {
		return fmt.Sprintf("%dh", d/time.Hour)
	}
	if d%time.Minute == 0 {
		return fmt.Sprintf("%dm", d/time.Minute)
	}
	if d%time.Second == 0 {
		return fmt.Sprintf("%ds", d/time.Second)
	}
	return d.String()
}

func parseYAMLDurationNode(node *yaml.Node) (time.Duration, bool, error) {
	if node == nil || node.Kind == 0 {
		return 0, false, nil
	}
	if node.Kind != yaml.ScalarNode {
		return 0, false, fmt.Errorf("duration must be a scalar")
	}
	raw := strings.TrimSpace(node.Value)
	if raw == "" || raw == "null" || raw == "~" {
		return 0, false, nil
	}
	if d, err := time.ParseDuration(raw); err == nil {
		return d, true, nil
	}
	ns, err := strconv.ParseInt(raw, 10, 64)
	if err != nil {
		return 0, false, fmt.Errorf("invalid duration %q", raw)
	}
	return time.Duration(ns), true, nil
}
