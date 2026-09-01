package oanbinding

import (
	"fmt"
	"strings"
)

// Paths says where the two halves of a binding key live in a payload.
//
// This is a NETWORK convention, not a deployment's preference: every participant
// has to agree, or two adapters disagree about what a binding key is and
// requests silently fail to match. So BecknV2 is the answer, and overriding is
// something an operator has to type deliberately -- absent means correct.
//
// The override exists for one situation: the spec moves a field and a deployment
// needs to track it without waiting for a release. It is deliberately not
// something to reach for otherwise.
type Paths struct {
	ProviderID     string
	CapabilityCode string
}

// BecknV2 is where core-v2.0.0-lts puts them.
var BecknV2 = Paths{
	ProviderID:     "message.contract.commitments[].offer.provider.id",
	CapabilityCode: "message.contract.commitments[].resources[].resourceAttributes.@type",
}

// arrayMarker flattens an array at that segment. It is the only operator the
// walk understands.
const arrayMarker = "[]"

// Validate refuses a pair that could never match, so a mistake surfaces where it
// was configured rather than as every request quietly going unserved.
func (p Paths) Validate() error {
	for name, path := range map[string]string{
		"providerIdAt":     p.ProviderID,
		"capabilityCodeAt": p.CapabilityCode,
	} {
		if strings.TrimSpace(path) == "" {
			return fmt.Errorf("oanbinding: %s is empty", name)
		}
		for _, segment := range strings.Split(path, ".") {
			if strings.TrimSpace(strings.TrimSuffix(segment, arrayMarker)) == "" {
				return fmt.Errorf("oanbinding: %s (%q) has a blank segment", name, path)
			}
		}
	}
	return nil
}

// valuesAt collects every string the path reaches.
//
// The grammar is two things: segments separated by ".", and a "[]" suffix
// meaning "this is an array, look in each element". No wildcards, no filters, no
// indices. Each of those would be another way to write something subtly wrong in
// config nobody reviews, to buy an expressiveness a payload shape has never
// needed.
func valuesAt(node any, path string) []string {
	return walk(node, strings.Split(path, "."))
}

func walk(node any, segments []string) []string {
	if len(segments) == 0 {
		// The leaf. Only strings are binding-key material; a number or an
		// object here means the path landed somewhere unintended.
		if value, ok := node.(string); ok {
			return []string{value}
		}
		return nil
	}

	segment := segments[0]
	rest := segments[1:]

	fields, ok := node.(map[string]any)
	if !ok {
		return nil
	}
	child, present := fields[strings.TrimSuffix(segment, arrayMarker)]
	if !present {
		return nil
	}

	if !strings.HasSuffix(segment, arrayMarker) {
		return walk(child, rest)
	}

	// An array segment: every element contributes.
	elements, ok := child.([]any)
	if !ok {
		return nil
	}
	var found []string
	for _, element := range elements {
		found = append(found, walk(element, rest)...)
	}
	return found
}
