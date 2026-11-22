package model

import (
	"errors"
	"testing"
	"time"
)

func TestSelectKeyRecord(t *testing.T) {
	now := time.Date(2026, 11, 23, 12, 0, 0, 0, time.UTC)
	rec := func(sub, key, status string) Subscription {
		return Subscription{Subscriber: Subscriber{SubscriberID: sub}, KeyID: key, Status: status, SigningPublicKey: sub + "-key"}
	}
	tests := []struct {
		name    string
		subs    []Subscription
		wantKey string
		wantErr error
	}{
		{"exact match", []Subscription{rec("victim", "k1", "SUBSCRIBED")}, "victim-key", nil},
		{"record without key id", []Subscription{rec("victim", "", "SUBSCRIBED")}, "victim-key", nil},
		{"empty status (DeDi)", []Subscription{rec("victim", "k1", "")}, "victim-key", nil},
		// GHSA-xvr2-rwvm-4g6v: another subscriber's record under this lookup.
		{"other subscriber's record only", []Subscription{rec("attacker", "k1", "SUBSCRIBED")}, "", ErrKeyRecordNotFound},
		{"other subscriber's record first", []Subscription{rec("attacker", "k1", "SUBSCRIBED"), rec("victim", "k1", "SUBSCRIBED")}, "victim-key", nil},
		{"record without subscriber id", []Subscription{rec("", "k1", "SUBSCRIBED")}, "", ErrKeyRecordNotFound},
		{"different key id", []Subscription{rec("victim", "k2", "SUBSCRIBED")}, "", ErrKeyRecordNotFound},
		{"empty answer", nil, "", ErrKeyRecordNotFound},
		// GHSA-8h5h-6653-979h: status is an allowlist.
		{"unsubscribed", []Subscription{rec("victim", "k1", "UNSUBSCRIBED")}, "", ErrKeyRecordNotUsable},
		{"unknown status", []Subscription{rec("victim", "k1", "REVOKED_BY_ADMIN")}, "", ErrKeyRecordNotUsable},
		{"lower case status", []Subscription{rec("victim", "k1", "subscribed")}, "victim-key", nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := SelectKeyRecord(tt.subs, "victim", "k1", now)
			if !errors.Is(err, tt.wantErr) {
				t.Fatalf("err=%v, want %v", err, tt.wantErr)
			}
			if tt.wantErr == nil && got.SigningPublicKey != tt.wantKey {
				t.Fatalf("selected %q, want %q", got.SigningPublicKey, tt.wantKey)
			}
		})
	}
	if _, err := SelectKeyRecord([]Subscription{rec("", "k1", "SUBSCRIBED")}, "", "k1", now); !errors.Is(err, ErrKeyRecordNotFound) {
		t.Fatalf("empty requested subscriber matched an empty record: %v", err)
	}
}

func TestSelectKeyRecordValidityWindow(t *testing.T) {
	now := time.Date(2026, 11, 23, 12, 0, 0, 0, time.UTC)
	base := Subscription{Subscriber: Subscriber{SubscriberID: "victim"}, KeyID: "k1", Status: "SUBSCRIBED"}
	tests := []struct {
		name        string
		from, until time.Time
		wantErr     error
	}{
		{"no window", time.Time{}, time.Time{}, nil},
		{"inside", now.Add(-time.Hour), now.Add(time.Hour), nil},
		{"expired, status still SUBSCRIBED", now.Add(-48 * time.Hour), now.Add(-time.Hour), ErrKeyRecordOutsideValidity},
		{"expires exactly now", time.Time{}, now, ErrKeyRecordOutsideValidity},
		{"not yet valid", now.Add(time.Hour), now.Add(48 * time.Hour), ErrKeyRecordOutsideValidity},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := base
			s.ValidFrom, s.ValidUntil = tt.from, tt.until
			if _, err := SelectKeyRecord([]Subscription{s}, "victim", "k1", now); !errors.Is(err, tt.wantErr) {
				t.Fatalf("err=%v, want %v", err, tt.wantErr)
			}
		})
	}
}
