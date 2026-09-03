// Package oanbinding derives the capability binding a Beckn request is asking
// for, so a provider step can tell whether the request is its work and, if it
// is, which registry row describes the call.
//
// It is shared by every provider step rather than living in one, because the
// binding is a property of the OAN network's payloads and not of any provider.
package oanbinding

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
)

// separator joins a binding key's two halves.
const separator = "|"

// ErrNoBinding reports a payload that names no capability binding. It is not a
// fault: a request for something else entirely reaches a provider step too, and
// the step's answer is to do nothing.
var ErrNoBinding = errors.New("oanbinding: payload names no capability binding")

// Binding identifies one provider capability.
type Binding struct {
	ParticipantID  string
	CapabilityCode string
}

// Key renders the binding in the form the registry indexes on.
func (b Binding) Key() string {
	return b.ParticipantID + separator + b.CapabilityCode
}

// From derives the capability binding a payload is asking for.
//
// Returns ErrNoBinding when the payload names no provider or no type, which is
// the ordinary case for a request a provider step is not meant to serve.
//
// A payload carrying more than one distinct provider or type is refused rather
// than resolved to its first: the two halves index one registry row describing
// one upstream call, so a request spanning several is asking for something this
// design cannot express. Guessing would silently serve part of it.
//
// Where the halves live is BecknV2 unless a deployment says otherwise -- see
// Paths for why that is a default and not a setting.
func From(paths Paths, body []byte) (Binding, error) {
	var payload any
	if err := json.Unmarshal(body, &payload); err != nil {
		return Binding{}, fmt.Errorf("oanbinding: payload could not be read: %w", err)
	}

	// Before distinctness: N commitments naming the SAME provider and type
	// collapse to one binding key, so they would resolve here without
	// complaint -- and then the mapping reads commitments[0] and the rest are
	// dropped, leaving the caller a confident, signed, spec-valid answer to
	// part of what it asked. One request maps to one call, so several is a
	// request this design cannot express and is refused rather than halved.
	providerValues := valuesAt(payload, paths.ProviderID)
	if len(providerValues) > 1 {
		return Binding{}, fmt.Errorf(
			"oanbinding: payload carries %d commitments; one request maps to one call, "+
				"so send them separately rather than have all but the first dropped",
			len(providerValues))
	}

	providers := distinct(providerValues)
	types := distinct(valuesAt(payload, paths.CapabilityCode))

	if len(providers) == 0 || len(types) == 0 {
		return Binding{}, ErrNoBinding
	}
	if len(providers) > 1 {
		return Binding{}, fmt.Errorf("oanbinding: payload names %d providers (%s); one request maps to one call",
			len(providers), strings.Join(providers, ", "))
	}
	if len(types) > 1 {
		return Binding{}, fmt.Errorf("oanbinding: payload names %d resource types (%s); one request maps to one call",
			len(types), strings.Join(types, ", "))
	}

	return Binding{ParticipantID: providers[0], CapabilityCode: types[0]}, nil
}

// distinct drops blanks and repeats, keeping the order they were found in so a
// refusal names them the way the payload did.
func distinct(values []string) []string {
	var out []string
	for _, value := range values {
		out = appendDistinct(out, value)
	}
	return out
}

// appendDistinct adds value if it is neither empty nor already present.
func appendDistinct(values []string, value string) []string {
	if value == "" {
		return values
	}
	for _, existing := range values {
		if existing == value {
			return values
		}
	}
	return append(values, value)
}
