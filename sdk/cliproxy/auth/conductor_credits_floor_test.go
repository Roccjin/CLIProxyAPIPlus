package auth

import (
	"context"
	"testing"

	"github.com/google/uuid"
)

func TestApplyBuddyCreditsFloor_ParksBelowFloor(t *testing.T) {
	t.Parallel()
	auth := &Auth{ID: "wb-" + uuid.NewString(), Provider: "workbuddy", Metadata: map[string]any{"type": "workbuddy"}}
	m := NewManager(nil, nil, nil)
	if _, err := m.Register(WithSkipPersist(context.Background()), auth); err != nil {
		t.Fatal(err)
	}
	if !m.ApplyBuddyCreditsFloor(context.Background(), auth.ID, 9, 10) {
		t.Fatal("expected reserve disable")
	}
	got, ok := m.GetByID(auth.ID)
	if !ok || !got.Disabled {
		t.Fatal("auth was not disabled")
	}
	if got.Metadata[MetadataKeyDisabledReason] != DisabledReasonCreditsReserve {
		t.Fatalf("reason = %#v", got.Metadata[MetadataKeyDisabledReason])
	}
	if _, exists := got.Metadata[MetadataKeyDisabledProviderCode]; exists {
		t.Fatalf("provider code = %#v", got.Metadata[MetadataKeyDisabledProviderCode])
	}
	if got.StatusMessage != CreditsReserveStatusMessage("workbuddy") {
		t.Fatalf("status = %q", got.StatusMessage)
	}
}

func TestApplyBuddyCreditsFloor_EqualFloorStaysActive(t *testing.T) {
	t.Parallel()
	auth := &Auth{ID: "cb-" + uuid.NewString(), Provider: "codebuddy", Metadata: map[string]any{"type": "codebuddy"}}
	m := NewManager(nil, nil, nil)
	if _, err := m.Register(WithSkipPersist(context.Background()), auth); err != nil {
		t.Fatal(err)
	}
	if m.ApplyBuddyCreditsFloor(context.Background(), auth.ID, 10, 10) {
		t.Fatal("equal remain disabled the auth")
	}
	got, _ := m.GetByID(auth.ID)
	if got.Disabled {
		t.Fatal("auth disabled at the floor")
	}
}

func TestApplyBuddyCreditsFloor_ZeroRemainExhausts(t *testing.T) {
	t.Parallel()
	auth := &Auth{ID: "wb-" + uuid.NewString(), Provider: "workbuddy", Metadata: map[string]any{"type": "workbuddy"}}
	m := NewManager(nil, nil, nil)
	if _, err := m.Register(WithSkipPersist(context.Background()), auth); err != nil {
		t.Fatal(err)
	}
	if !m.ApplyBuddyCreditsFloor(context.Background(), auth.ID, 0, 10) {
		t.Fatal("expected exhausted disable")
	}
	got, _ := m.GetByID(auth.ID)
	if got.Metadata[MetadataKeyDisabledReason] != DisabledReasonCreditsExhausted {
		t.Fatalf("reason = %#v", got.Metadata[MetadataKeyDisabledReason])
	}
	if got.Metadata[MetadataKeyDisabledProviderCode] != "14018" {
		t.Fatalf("provider code = %#v", got.Metadata[MetadataKeyDisabledProviderCode])
	}
}

func TestApplyBuddyCreditsFloor_DefaultFloorDoesNotDisable(t *testing.T) {
	t.Parallel()
	auth := &Auth{ID: "wb-" + uuid.NewString(), Provider: "workbuddy", Metadata: map[string]any{"type": "workbuddy"}}
	m := NewManager(nil, nil, nil)
	if _, err := m.Register(WithSkipPersist(context.Background()), auth); err != nil {
		t.Fatal(err)
	}
	if m.ApplyBuddyCreditsFloor(context.Background(), auth.ID, 0, 1) {
		t.Fatal("floor 1 disabled the auth")
	}
}

func TestApplyBuddyCreditsFloor_ManualDisableWins(t *testing.T) {
	t.Parallel()
	auth := &Auth{
		ID:       "wb-" + uuid.NewString(),
		Provider: "workbuddy",
		Disabled: true,
		Status:   StatusDisabled,
		Metadata: map[string]any{"type": "workbuddy", "disabled": true, MetadataKeyDisabledReason: "manual"},
	}
	m := NewManager(nil, nil, nil)
	if _, err := m.Register(WithSkipPersist(context.Background()), auth); err != nil {
		t.Fatal(err)
	}
	if m.ApplyBuddyCreditsFloor(context.Background(), auth.ID, 3, 10) {
		t.Fatal("manual disable was replaced")
	}
	got, _ := m.GetByID(auth.ID)
	if got.Metadata[MetadataKeyDisabledReason] != "manual" {
		t.Fatalf("reason = %#v", got.Metadata[MetadataKeyDisabledReason])
	}
}
