package auth

import (
	"context"
	"strings"
	"time"
)

// buddyReserveObserver is stored in an atomic.Value, so the concrete type must stay stable.
type buddyReserveObserver struct {
	fn func(authID, provider string)
}

// SetBuddyReserveObserver installs the host callback invoked after a successful
// execution. A nil function clears it. The callback must return quickly.
func (m *Manager) SetBuddyReserveObserver(fn func(authID, provider string)) {
	if m == nil {
		return
	}
	m.reserveObserver.Store(&buddyReserveObserver{fn: fn})
}

func (m *Manager) notifyBuddyReserve(authID, provider string) {
	if m == nil {
		return
	}
	slot, _ := m.reserveObserver.Load().(*buddyReserveObserver)
	if slot == nil || slot.fn == nil {
		return
	}
	slot.fn(authID, provider)
}

// ApplyBuddyCreditsFloor parks a Buddy auth when a post-call billing sample is
// below the configured floor. floor <= 1 leaves the 14018-only disable path
// unchanged. remain <= 0 records credits_exhausted. A positive remain below
// the floor records credits_reserve. Manual disables and an existing
// credits_exhausted reason are left as they are.
func (m *Manager) ApplyBuddyCreditsFloor(ctx context.Context, authID string, remain, floor float64) bool {
	if m == nil || strings.TrimSpace(authID) == "" || floor <= 1 || remain >= floor {
		return false
	}
	if ctx == nil {
		ctx = context.Background()
	}
	now := time.Now()
	m.mu.Lock()
	auth := m.auths[authID]
	if auth == nil {
		m.mu.Unlock()
		return false
	}
	applied := false
	if remain <= 0 {
		applied = applyCreditsExhaustedDisable(auth, &Error{
			Code:    ErrorCodeCredentialCreditsExhausted,
			Message: creditsExhaustedStatusMessage(auth.Provider),
		}, now)
	} else {
		applied = applyCreditsReserveDisable(auth, now)
	}
	if !applied {
		m.mu.Unlock()
		return false
	}
	if errPersist := m.persist(ctx, auth); errPersist != nil {
		logEntryWithRequestID(ctx).WithField("auth_id", authID).Warnf("failed to persist credits floor: %v", errPersist)
	}
	snapshot := auth.Clone()
	m.mu.Unlock()
	if m.scheduler != nil && snapshot != nil {
		m.scheduler.upsertAuth(snapshot)
	}
	m.queueRefreshUnschedule(authID)
	m.invalidateSessionAffinity(authID)
	return true
}

func applyCreditsReserveDisable(auth *Auth, now time.Time) bool {
	if auth == nil {
		return false
	}
	existingReason := disabledReason(auth)
	if authDisabledFromState(auth) && existingReason != DisabledReasonCreditsReserve {
		return false
	}
	if existingReason == DisabledReasonCreditsReserve && auth.Disabled {
		return false
	}
	for _, state := range auth.ModelStates {
		resetModelState(state, now)
	}
	auth.Unavailable = false
	auth.NextRetryAfter = time.Time{}
	auth.Quota = QuotaState{}
	auth.Disabled = true
	auth.Status = StatusDisabled
	auth.StatusMessage = CreditsReserveStatusMessage(auth.Provider)
	auth.LastError = nil
	auth.UpdatedAt = now
	if auth.Metadata == nil {
		auth.Metadata = make(map[string]any)
	}
	auth.Metadata["disabled"] = true
	auth.Metadata[MetadataKeyDisabledReason] = DisabledReasonCreditsReserve
	delete(auth.Metadata, MetadataKeyDisabledProviderCode)
	if raw, ok := auth.Metadata[MetadataKeyDisabledAt].(string); !ok || strings.TrimSpace(raw) == "" {
		auth.Metadata[MetadataKeyDisabledAt] = now.UTC().Format(time.RFC3339Nano)
	}
	return true
}

func disabledReason(auth *Auth) string {
	if auth == nil || auth.Metadata == nil {
		return ""
	}
	raw, ok := auth.Metadata[MetadataKeyDisabledReason].(string)
	if !ok {
		return ""
	}
	return strings.TrimSpace(raw)
}
