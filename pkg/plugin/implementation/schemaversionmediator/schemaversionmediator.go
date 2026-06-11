// Package schemaversionmediator implements the SchemaVersionMediator plugin.
// It walks inbound Beckn payloads, checks schema object compatibility against
// the local node manifest, and dispatches translation for incompatible objects.
package schemaversionmediator

import (
	"encoding/json"
	"errors"
	"fmt"

	"github.com/beckn-one/beckn-onix/pkg/model"
)

// PolicyAction defines what the mediator does when schema incompatibility is
// detected or when a translation attempt fails.
type PolicyAction string

const (
	// PolicyActionReject rejects the request immediately with a NACK.
	PolicyActionReject PolicyAction = "reject"
	// PolicyActionTranslate attempts translation for each incompatible schema
	// object. On failure the OnFailure policy applies.
	PolicyActionTranslate PolicyAction = "translate"
	// PolicyActionPassIncompatible forwards the request as-is with a structured
	// log signal indicating which schema objects were not translated.
	PolicyActionPassIncompatible PolicyAction = "pass_incompatible"
)

// TranslationPolicy governs mediator behaviour when schema incompatibilities
// are found. It is loaded from the plugin config map and applied by Mediate.
//
// Action is evaluated immediately after CheckCompatibility returns incompatible
// objects — before any translation is attempted. OnFailure is only consulted
// when Action is PolicyActionTranslate and the translation attempt fails (no
// artifact found, or execution error).
type TranslationPolicy struct {
	Action    PolicyAction
	OnFailure PolicyAction
}

// defaultPolicy is the sentinel default when the operator has not configured a policy.
// translate/reject is the safest default: attempt translation, hard-fail if
// it cannot be completed, never silently forward an untranslated payload.
// Declared as a value (not a pointer) to prevent accidental mutation.
var defaultPolicy = TranslationPolicy{
	Action:    PolicyActionTranslate,
	OnFailure: PolicyActionReject,
}

// loadTranslationPolicy reads the mediator policy from the plugin config map.
// Config keys: "action" and "onFailure". Both are optional — absent keys fall
// back to the default policy (translate/reject).
//
// Valid values for action:    reject | translate | pass_incompatible
// Valid values for onFailure: reject | pass_incompatible (only validated when action=translate;
// ignored otherwise since no translation is ever attempted)
// Setting onFailure to "translate" is not permitted — it would cause a loop.
func loadTranslationPolicy(config map[string]string) (*TranslationPolicy, error) {
	p := &TranslationPolicy{
		Action:    defaultPolicy.Action,
		OnFailure: defaultPolicy.OnFailure,
	}

	if raw, ok := config["action"]; ok {
		switch PolicyAction(raw) {
		case PolicyActionReject, PolicyActionTranslate, PolicyActionPassIncompatible:
			p.Action = PolicyAction(raw)
		default:
			return nil, fmt.Errorf("schemaversionmediator: invalid action %q: must be reject, translate, or pass_incompatible", raw)
		}
	}

	// onFailure is only meaningful when action=translate. Validate it only in
	// that case — silently ignoring it for other actions avoids surprising errors
	// when operators carry over a stale onFailure key alongside action=reject.
	if p.Action == PolicyActionTranslate {
		if raw, ok := config["onFailure"]; ok {
			switch PolicyAction(raw) {
			case PolicyActionReject, PolicyActionPassIncompatible:
				p.OnFailure = PolicyAction(raw)
			case PolicyActionTranslate:
				return nil, fmt.Errorf("schemaversionmediator: onFailure cannot be %q — would cause a translation loop", raw)
			default:
				return nil, fmt.Errorf("schemaversionmediator: invalid onFailure %q: must be reject or pass_incompatible", raw)
			}
		}
	}

	return p, nil
}

// ErrNoManifest is returned by CheckCompatibility when the node manifest is nil.
// The caller should log a warning and skip mediation — translation targets cannot
// be determined without a manifest, but the absence of one is not a hard failure.
var ErrNoManifest = errors.New("schemaversionmediator: node manifest unavailable, skipping mediation")

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
// depth-first and collects every qualifying node regardless of nesting level,
// including both a parent node and its nested children when both carry
// "@context"/"@type" declarations — each is an independent schema contract.
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
// When a map node carries both "@context" and "@type" it is collected, then
// the walk continues into its children — a parent and its nested children may
// each declare independent schema objects and both are valid collection targets.
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
		for key, child := range v {
			// Skip the JSON-LD marker fields themselves — they are strings and
			// descending into them is a no-op, but skipping makes intent explicit.
			if key == "@context" || key == "@type" {
				continue
			}
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
// Returns ErrNoManifest if manifest is nil — the caller should log a warning
// and skip mediation rather than treating this as a hard failure.
//
// For each extracted SchemaObject:
//   - Exact match in manifest → compatible, omitted from result.
//   - Same Type, different ContextURL → TranslationNeeded with To set to the
//     locally supported SchemaObject (version the node expects).
//   - Type absent from manifest entirely → TranslationNeeded with To nil;
//     handling is delegated to the data-loss policy enforcer.
func CheckCompatibility(extracted []model.SchemaObject, manifest *model.NodeManifest) ([]TranslationNeeded, error) {
	if manifest == nil {
		return nil, ErrNoManifest
	}

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
	return needs, nil
}
