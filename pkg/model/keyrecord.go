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

// validFromLeeway is how far ahead of this adapter's clock a record's
// valid_from may be and still count as started. Registries and adapters do
// not share a clock.
const validFromLeeway = time.Minute

// SameSubscriberID reports whether two subscriber IDs name the same
// subscriber. Subscriber IDs are host names, so letter case and a trailing
// dot do not distinguish them.
func SameSubscriberID(a, b string) bool {
	a, b = strings.TrimSuffix(strings.TrimSpace(a), "."), strings.TrimSuffix(strings.TrimSpace(b), ".")
	return a != "" && strings.EqualFold(a, b)
}

// SelectKeyRecord picks, from a registry lookup answer, the record whose key
// may be used to verify a signature made as subscriberID with keyID.
//
// A registry answer is not trusted to be about the subscriber that was asked
// for: a record for anyone else is ignored, so one subscriber's key can never
// verify a message sent in another's name. A record that names a different
// key id is ignored too. Among the records that remain, one that names the
// requested key id is preferred over one that names none, and the first one
// with a usable status inside its validity window is returned. If none is
// usable, the error says why the best candidate was refused.
func SelectKeyRecord(subs []Subscription, subscriberID, keyID string, now time.Time) (*Subscription, error) {
	var candidates []*Subscription
	for pass := 0; pass < 2; pass++ {
		for i := range subs {
			s := &subs[i]
			if !SameSubscriberID(s.SubscriberID, subscriberID) {
				continue
			}
			named := s.KeyID != "" && keyID != ""
			if named && !strings.EqualFold(s.KeyID, keyID) {
				continue
			}
			// Pass 0: records that name the requested key. Pass 1: the rest.
			if (pass == 0) == named {
				candidates = append(candidates, s)
			}
		}
	}
	if len(candidates) == 0 {
		return nil, ErrKeyRecordNotFound
	}
	var firstErr error
	for _, s := range candidates {
		err := keyRecordUsable(s, now)
		if err == nil {
			return s, nil
		}
		if firstErr == nil {
			firstErr = err
		}
	}
	return nil, firstErr
}

func keyRecordUsable(s *Subscription, now time.Time) error {
	if !IsKeyStatusUsable(s.Status) {
		return fmt.Errorf("%w: status %q", ErrKeyRecordNotUsable, s.Status)
	}
	if !s.ValidFrom.IsZero() && now.Add(validFromLeeway).Before(s.ValidFrom) {
		return fmt.Errorf("%w: valid from %s", ErrKeyRecordOutsideValidity, s.ValidFrom.UTC().Format(time.RFC3339))
	}
	if !s.ValidUntil.IsZero() && !now.Before(s.ValidUntil) {
		return fmt.Errorf("%w: valid until %s", ErrKeyRecordOutsideValidity, s.ValidUntil.UTC().Format(time.RFC3339))
	}
	return nil
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
