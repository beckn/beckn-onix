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

// selectPayload is the part of a Beckn v2 payload a binding is derived from.
//
// Both commitments and resources are arrays, and both are read as such. The
// provider is named once per commitment and the type once per resource, so a
// single request can in principle carry several -- see From for what happens
// when it does.
type selectPayload struct {
	Message struct {
		Contract struct {
			Commitments []struct {
				Offer struct {
					Provider struct {
						ID string `json:"id"`
					} `json:"provider"`
				} `json:"offer"`
				Resources []struct {
					ResourceAttributes struct {
						Type string `json:"@type"`
					} `json:"resourceAttributes"`
				} `json:"resources"`
			} `json:"commitments"`
		} `json:"contract"`
	} `json:"message"`
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
func From(body []byte) (Binding, error) {
	var payload selectPayload
	if err := json.Unmarshal(body, &payload); err != nil {
		return Binding{}, fmt.Errorf("oanbinding: payload could not be read: %w", err)
	}

	var providers, types []string
	for _, commitment := range payload.Message.Contract.Commitments {
		providers = appendDistinct(providers, commitment.Offer.Provider.ID)
		for _, resource := range commitment.Resources {
			types = appendDistinct(types, resource.ResourceAttributes.Type)
		}
	}

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
