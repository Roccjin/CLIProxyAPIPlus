package registry

import "testing"

func TestPreserveNonEmptyModelSections_KeepsQoderWhenRemoteEmpty(t *testing.T) {
	oldData := &staticModelsJSON{
		Qoder: []*ModelInfo{{ID: "qoder/dfmodel"}},
		XAI:   []*ModelInfo{{ID: "grok-4"}},
	}
	newData := &staticModelsJSON{
		Qoder: nil,
		XAI:   []*ModelInfo{{ID: "grok-4"}},
	}
	preserveNonEmptyModelSections(oldData, newData)
	if len(newData.Qoder) != 1 || newData.Qoder[0].ID != "qoder/dfmodel" {
		t.Fatalf("qoder = %#v", newData.Qoder)
	}
	changed := detectChangedProviders(oldData, newData)
	for _, provider := range changed {
		if provider == "qoder" {
			t.Fatalf("qoder should not be marked changed when remote section is empty: %v", changed)
		}
	}
}

func TestPreserveNonEmptyModelSections_AllowsRemoteQoderUpdate(t *testing.T) {
	oldData := &staticModelsJSON{
		Qoder: []*ModelInfo{{ID: "qoder/gm51model"}},
	}
	newData := &staticModelsJSON{
		Qoder: []*ModelInfo{{ID: "qoder/gmodel"}},
	}
	preserveNonEmptyModelSections(oldData, newData)
	if len(newData.Qoder) != 1 || newData.Qoder[0].ID != "qoder/gmodel" {
		t.Fatalf("qoder = %#v", newData.Qoder)
	}
}
