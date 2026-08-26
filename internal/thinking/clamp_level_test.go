package thinking

import (
	"testing"

	"github.com/router-for-me/CLIProxyAPI/v7/internal/registry"
)

func TestClampLevel_XHighPrefersMaxWhenTiedWithHigh(t *testing.T) {
	info := &registry.ModelInfo{
		ID:       "qoder/dfmodel",
		Type:     "qoder",
		Thinking: &registry.ThinkingSupport{Levels: []string{"low", "high", "max"}},
	}
	if got := clampLevel(LevelXHigh, info, "openai"); got != LevelMax {
		t.Fatalf("clampLevel(xhigh) = %q, want max", got)
	}
}

func TestClampLevel_MediumStillPrefersLowerOnTie(t *testing.T) {
	info := &registry.ModelInfo{
		ID:       "qoder/dfmodel",
		Type:     "qoder",
		Thinking: &registry.ThinkingSupport{Levels: []string{"low", "high"}},
	}
	if got := clampLevel(LevelMedium, info, "openai"); got != LevelLow {
		t.Fatalf("clampLevel(medium) = %q, want low", got)
	}
}
