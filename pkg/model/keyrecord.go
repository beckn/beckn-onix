package model

import (
	"errors"
	"fmt"
	"strings"
	"time"
)

// Reasons SelectKeyRecord refuses a registry answer.
var (
	// ErrKeyRecordNotFound: no returned record is for the requested subscriber and key.
	ErrKeyRecordNotFound = errors.New("registry has no record for the requested subscriber and key")
	// ErrKeyRecordNotUsable: the record's status does not permit use of the key.
	ErrKeyRecordNotUsable = errors.New("registry record status does not permit use of the key")
	// ErrKeyRecordOutsideValidity: now is before valid_from or at/after valid_until.
	ErrKeyRecordOutsideValidity = errors.New("registry record is outside its validity window")
)

// SelectKeyRecord picks, from a registry lookup answer, the record whose key
// may be used to verify a signature made as subscriberID with keyID.
//
// A registry answer is not trusted to be about the subscriber that was asked
// for: a record for anyone else is ignored, so one subscriber's key can never
// verify a message sent in another's name. A record that names a different
// key id is ignored too. The chosen record must have a usable status and be
// inside its validity window.
func SelectKeyRecord(subs []Subscription, subscriberID, keyID string, now time.Time) (*Subscription, error) {
	var match *Subscription
	for i := range subs {
		s := &subs[i]
		if subscriberID == "" || s.SubscriberID != subscriberID {
			continue
		}
		if keyID != "" && s.KeyID != "" && s.KeyID != keyID {
			continue
		}
		match = s
		break
	}
	if match == nil {
		return nil, ErrKeyRecordNotFound
	}
	if !IsKeyStatusUsable(match.Status) {
		return nil, fmt.Errorf("%w: status %q", ErrKeyRecordNotUsable, match.Status)
	}
	if !match.ValidFrom.IsZero() && now.Before(match.ValidFrom) {
		return nil, fmt.Errorf("%w: valid from %s", ErrKeyRecordOutsideValidity, match.ValidFrom.UTC().Format(time.RFC3339))
	}
	if !match.ValidUntil.IsZero() && !now.Before(match.ValidUntil) {
		return nil, fmt.Errorf("%w: valid until %s", ErrKeyRecordOutsideValidity, match.ValidUntil.UTC().Format(time.RFC3339))
	}
	return match, nil
}

// usableKeySubscriptionStatuses are the Subscription.Status values under which
// a subscriber's key may verify signatures. The empty status is included
// because the DeDi registry has no status field; its record state is checked
// by the DeDi client instead. INITIATED and UNDER_SUBSCRIPTION are included
// because this check separates revocation and expiry from good standing; it
// does not gate on onboarding being complete. Any other value, including one
// this code does not know, is not usable.
var usableKeySubscriptionStatuses = map[string]bool{
	"":                   true,
	"SUBSCRIBED":         true,
	"INITIATED":          true,
	"UNDER_SUBSCRIPTION": true,
}

// IsKeyStatusUsable reports whether a subscriber's registry Status permits
// its key to be used for signature verification. Unknown statuses are not
// usable.
func IsKeyStatusUsable(status string) bool {
	return usableKeySubscriptionStatuses[strings.ToUpper(strings.TrimSpace(status))]
}
