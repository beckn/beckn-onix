// Package schemaversionmediator implements the SchemaVersionMediator plugin.
// It walks inbound Beckn payloads, checks schema object compatibility against
// the local node manifest, and dispatches translation for incompatible objects.
package schemaversionmediator

import (
	"encoding/json"
	"fmt"

	"github.com/beckn-one/beckn-onix/pkg/model"
)

// TranslationNeeded describes a single schema object from the payload that
// the local node cannot handle as-is and requires translation.
//
// From is the schema object as declared in the inbound payload.
// To is the schema object the local node supports for the same Type.
// To is nil when the Type is entirely absent from the local node manifest —
// an unknown schema whose handling is governed by the data-loss policy.
type TranslationNeeded struct {
	From model.SchemaObject
	To   *model.SchemaObject
}

// WalkPayload recursively traverses a JSON payload and returns all schema
// objects declared via JSON-LD "@context" and "@type" fields. The walk is
// depth-first and collects every qualifying node regardless of nesting level.
// The payload is not modified.
func WalkPayload(payload []byte) ([]model.SchemaObject, error) {
	var root any
	if err := json.Unmarshal(payload, &root); err != nil {
		return nil, fmt.Errorf("schemaversionmediator: walk payload: %w", err)
	}
	var results []model.SchemaObject
	walkNode(root, &results)
	return results, nil
}

// walkNode is the recursive descent worker for WalkPayload.
func walkNode(node any, results *[]model.SchemaObject) {
	switch v := node.(type) {
	case map[string]any:
		if contextURL, ok := stringField(v, "@context"); ok {
			if typ, ok := stringField(v, "@type"); ok {
				*results = append(*results, model.SchemaObject{
					ContextURL: contextURL,
					Type:       typ,
				})
			}
		}
		for _, child := range v {
			walkNode(child, results)
		}
	case []any:
		for _, item := range v {
			walkNode(item, results)
		}
	}
}

// stringField returns the string value of key in m, reporting whether it was
// present and non-empty.
func stringField(m map[string]any, key string) (string, bool) {
	v, ok := m[key]
	if !ok {
		return "", false
	}
	s, ok := v.(string)
	return s, ok && s != ""
}

// CheckCompatibility compares extracted schema objects against the local node
// manifest and returns those that require translation. An empty result means
// the payload is fully compatible and the mediator can short-circuit.
//
// For each extracted SchemaObject:
//   - Exact match in manifest → compatible, omitted from result.
//   - Same Type, different ContextURL → TranslationNeeded with To set to the
//     locally supported SchemaObject (version the node expects).
//   - Type absent from manifest entirely → TranslationNeeded with To nil;
//     handling is delegated to the data-loss policy enforcer.
func CheckCompatibility(extracted []model.SchemaObject, manifest *model.NodeManifest) []TranslationNeeded {
	supported := make(map[string]model.SchemaObject, len(manifest.Schema.SchemaObjects))
	for _, obj := range manifest.Schema.SchemaObjects {
		supported[obj.Type] = obj
	}

	var needs []TranslationNeeded
	for _, from := range extracted {
		local, known := supported[from.Type]
		switch {
		case !known:
			// Type entirely absent from the local manifest — unknown schema.
			needs = append(needs, TranslationNeeded{From: from})
		case local.ContextURL != from.ContextURL:
			// Same type, different version — translation required.
			to := local
			needs = append(needs, TranslationNeeded{From: from, To: &to})
		}
		// Exact match: compatible — no entry added.
	}
	return needs
}
