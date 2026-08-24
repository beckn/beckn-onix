package catalogpublisher

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"

	"github.com/beckn-one/beckn-onix/core/module/handler"
	"github.com/beckn-one/beckn-onix/pkg/plugin/definition"
	"github.com/santhosh-tekuri/jsonschema/v6"
)

// publishDirective is one entry in publishRequest.Message.PublishDirectives,
// matched to a submitted catalog by CatalogID -- beckn.yaml's own
// CatalogPublishAction.publishDirectives shape. VisibleTo is the spec's
// name for what the catalog-index file itself (and CatalogSubmission)
// call NetworkIds -- restricts delivery of this catalog to the listed
// network participant ids; omitted means visible to all. CatalogType is
// required by the spec (unlike CatalogSubmission.CatalogType, which
// defaults to "REGULAR" when empty); callers must set it explicitly once
// schemaValidator is configured. UpdateMode/ResourceDirectives are
// validated (see validatePublishRequest) but not otherwise acted on:
// UpdateMode's FULL/MERGE semantics and ResourceDirectives' master/
// variant resolution are resolved by the Discovery Service at index
// time, not centrally at publish time (see this package's README,
// "What changes at each layer"). SchemaTypes is not a beckn.yaml field at
// all -- it's an NFH-014 addition to the catalog index entry (see
// validateSchemaTypes) -- carried here so a caller has one place to set
// every per-catalog directive field.
type publishDirective struct {
	CatalogID          string              `json:"catalogId"`
	VisibleTo          []string            `json:"visibleTo,omitempty"`
	CatalogType        string              `json:"catalogType,omitempty"`
	UpdateMode         string              `json:"updateMode,omitempty"`
	ResourceDirectives []resourceDirective `json:"resourceDirectives,omitempty"`
	SchemaTypes        []string            `json:"schemaTypes,omitempty"`
}

// resourceDirective links one resource in a REGULAR catalog to the master
// resource it extends, per beckn.yaml's
// CatalogPublishAction.publishDirectives[].resourceDirectives shape.
type resourceDirective struct {
	ResourceID string `json:"resourceId"`
	Extends    struct {
		MasterResourceID string `json:"masterResourceId"`
	} `json:"extends"`
}

// publishRequest is the DS-facing catalog/publish request body. It matches
// beckn.yaml's real CatalogPublishAction envelope shape (context/message,
// message.catalogs[]/publishDirectives[]) so schemaValidator/policyChecker
// (wired by the generic handler, not this plugin) can validate the raw
// request body directly -- no synthesized envelope needed. Context carries
// only "action": this is still a DS-internal, unsigned, same-operator
// trigger, not a signed/routed network-facing Beckn action, so none of
// Context's other fields (bapId/bapUri/messageId/...) are meaningful here
// and the real spec leaves all of them optional. Retire/ForceBaseline are
// this plugin's own additions, with no beckn.yaml equivalent -- kept as
// siblings of context/message rather than invented fields inside either.
type publishRequest struct {
	Context struct {
		Action string `json:"action"`
	} `json:"context"`
	Message struct {
		Catalogs          []json.RawMessage  `json:"catalogs"`
		PublishDirectives []publishDirective `json:"publishDirectives,omitempty"`
	} `json:"message"`
	Retire        []string `json:"retire,omitempty"`
	ForceBaseline bool     `json:"forceBaseline,omitempty"`
}

// validatePublishRequest checks req is referentially/structurally sound
// beyond plain JSON decoding: at least one catalog or retire entry must
// be present, plus every referential and business-rule constraint a
// JSON Schema can't express -- a JSON Schema has no way to assert one
// array's values are drawn from another array in the same document --
// so these run unconditionally, independent of whether schemaValidator
// is even configured. Collects every violation found rather than
// stopping at the first, so a caller can fix a malformed request in one
// round trip. Per-catalog structural issues (missing/empty id, bad
// descriptor) are deliberately left to Publish's own validate stage,
// which reports them as a non-fatal PublishError per catalog rather than
// failing the whole request -- this function only rejects requests whose
// cross-references or unsupported-but-well-formed values make them
// impossible to process correctly at all.
func validatePublishRequest(req publishRequest) error {
	if len(req.Message.Catalogs) == 0 && len(req.Retire) == 0 {
		return fmt.Errorf("message.catalogs or retire is required")
	}

	type catalogInfo struct {
		resourceIDs map[string]bool
	}
	catalogs := make(map[string]catalogInfo, len(req.Message.Catalogs))
	var errs []error

	for _, raw := range req.Message.Catalogs {
		var probe struct {
			ID        string `json:"id"`
			Resources []struct {
				ID string `json:"id"`
			} `json:"resources"`
		}
		_ = json.Unmarshal(raw, &probe) // malformed/missing id surfaces as a non-fatal PublishError downstream, not a rejection here
		if probe.ID == "" {
			continue
		}
		if _, dup := catalogs[probe.ID]; dup {
			errs = append(errs, fmt.Errorf("duplicate catalog id %q in message.catalogs", probe.ID))
			continue
		}
		resourceIDs := make(map[string]bool, len(probe.Resources))
		for _, res := range probe.Resources {
			if res.ID != "" {
				resourceIDs[res.ID] = true
			}
		}
		catalogs[probe.ID] = catalogInfo{resourceIDs: resourceIDs}
	}

	seenDirectives := make(map[string]bool, len(req.Message.PublishDirectives))
	for _, d := range req.Message.PublishDirectives {
		if d.CatalogID == "" {
			continue // caught by schemaValidator's "required" when configured; nothing referential to check here
		}
		if seenDirectives[d.CatalogID] {
			errs = append(errs, fmt.Errorf("duplicate publishDirectives entry for catalogId %q", d.CatalogID))
			continue
		}
		seenDirectives[d.CatalogID] = true

		info, ok := catalogs[d.CatalogID]
		if !ok {
			errs = append(errs, fmt.Errorf("publishDirectives entry for catalogId %q does not match any submitted catalog", d.CatalogID))
			continue
		}

		switch d.UpdateMode {
		case "", "MERGE":
		case "FULL":
			errs = append(errs, fmt.Errorf("publishDirectives entry for catalogId %q: updateMode \"FULL\" is not yet supported (only \"MERGE\")", d.CatalogID))
		default:
			errs = append(errs, fmt.Errorf("publishDirectives entry for catalogId %q: invalid updateMode %q (must be \"MERGE\" or \"FULL\")", d.CatalogID, d.UpdateMode))
		}

		for _, rd := range d.ResourceDirectives {
			if rd.ResourceID == "" {
				continue
			}
			if !info.resourceIDs[rd.ResourceID] {
				errs = append(errs, fmt.Errorf("publishDirectives entry for catalogId %q: resourceDirectives resourceId %q not found in that catalog's resources", d.CatalogID, rd.ResourceID))
			}
			if rd.Extends.MasterResourceID == "" {
				errs = append(errs, fmt.Errorf("publishDirectives entry for catalogId %q: resourceDirectives resourceId %q missing extends.masterResourceId", d.CatalogID, rd.ResourceID))
			}
		}

		if err := validateSchemaTypes(d.CatalogID, d.SchemaTypes); err != nil {
			errs = append(errs, err)
		}
	}

	return errors.Join(errs...)
}

// DecodeRequest implements definition.CatalogPublisher. It owns every
// HTTP-semantic/wire-shape decision for this request: method check, body
// read, JSON decoding, and validatePublishRequest. Errors here always mean
// a malformed/invalid request. It deliberately does not run
// schemaValidator/policyChecker against the raw body -- those are generic
// plugins wired by NewHandler (handler.go), unrelated to CatalogPublisher's
// own contract. The method-mismatch case is wrapped in a
// handler.StatusError so the generic EndpointHandler surfaces it as 405,
// not the default 400 every other decode failure here gets.
func (p *Publisher) DecodeRequest(ctx context.Context, r *http.Request) (definition.PublishRequest, error) {
	if r.Method != http.MethodPost {
		return definition.PublishRequest{}, &handler.StatusError{
			Status: http.StatusMethodNotAllowed,
			Err:    fmt.Errorf("method not allowed: %s", r.Method),
		}
	}
	body, err := io.ReadAll(r.Body)
	if err != nil {
		return definition.PublishRequest{}, fmt.Errorf("reading request body: %w", err)
	}
	var req publishRequest
	if err := json.Unmarshal(body, &req); err != nil {
		return definition.PublishRequest{}, fmt.Errorf("invalid request body: %w", err)
	}
	if err := validatePublishRequest(req); err != nil {
		return definition.PublishRequest{}, fmt.Errorf("invalid request: %w", err)
	}

	directives := make(map[string]publishDirective, len(req.Message.PublishDirectives))
	for _, d := range req.Message.PublishDirectives {
		directives[d.CatalogID] = d
	}

	submissions := make([]definition.CatalogSubmission, 0, len(req.Message.Catalogs))
	for _, raw := range req.Message.Catalogs {
		var probe struct {
			ID string `json:"id"`
		}
		_ = json.Unmarshal(raw, &probe) // empty ID surfaces as a non-fatal PublishError from Publish
		d := directives[probe.ID]
		submissions = append(submissions, definition.CatalogSubmission{
			CatalogID:   probe.ID,
			Catalog:     raw,
			NetworkIds:  d.VisibleTo,
			CatalogType: d.CatalogType,
			SchemaTypes: d.SchemaTypes,
		})
	}

	return definition.PublishRequest{
		Catalogs:      submissions,
		Retire:        req.Retire,
		ForceBaseline: req.ForceBaseline,
	}, nil
}

// schemaTypesSchemaID is this validator's own internal $id -- never
// fetched externally, just a stable name to compile/reference the
// embedded schema under.
const schemaTypesSchemaID = "https://beckn-onix.internal/schemas/nfh014-schema-types.json"

// schemaTypesSchemaJSON is a custom JSON Schema for
// publishDirectives[].schemaTypes, sourced from RFC NFH-014
// (Decentralized Catalog Publishing and Discovery). beckn.yaml's own
// CatalogPublishAction has no schemaTypes field at all -- it's an
// NFH-014 addition to the catalog index entry, not part of the canonical
// action envelope -- so there is no upstream machine-readable schema to
// point schemaValidator's own auxiliary-spec mechanism at; that
// mechanism also can't help here regardless, since auxiliary specs may
// only add actions beckn.yaml doesn't already define, never extend one
// it does (schemav2validator.loadAllSpecs hard-rejects a collision).
//
// Per NFH-014 ("schemaTypes ... Which domain schema(s) the catalog's
// content conforms to -- the same filtering purpose as networkIds, for a
// DS that only cares about specific domains"): an array of unique,
// non-empty domain-schema context URIs -- see the file spec's own
// examples, e.g. "https://schema.beckn.org/retail/schema/1.1.0/context.jsonld".
const schemaTypesSchemaJSON = `{
	"$id": "` + schemaTypesSchemaID + `",
	"$schema": "http://json-schema.org/draft-07/schema#",
	"title": "NFH-014 schemaTypes",
	"type": "array",
	"items": { "type": "string", "format": "uri", "minLength": 1 },
	"uniqueItems": true
}`

// schemaTypesSchemaOnce compiles schemaTypesSchemaJSON lazily, on first
// use, rather than as a package-level var initializer -- a compile
// failure (e.g. a future edit to schemaTypesSchemaJSON introducing a
// syntax error) then surfaces as an ordinary error from
// validateSchemaTypes instead of a panic at package load time that
// would crash every binary importing this package, not just this one
// validation path.
var (
	schemaTypesSchemaOnce sync.Once
	schemaTypesSchema     *jsonschema.Schema
	schemaTypesSchemaErr  error
)

func compileSchemaTypesSchema() (*jsonschema.Schema, error) {
	schemaTypesSchemaOnce.Do(func() {
		doc, err := jsonschema.UnmarshalJSON(strings.NewReader(schemaTypesSchemaJSON))
		if err != nil {
			schemaTypesSchemaErr = fmt.Errorf("catalogpublisher: parsing embedded schemaTypes schema: %w", err)
			return
		}
		compiler := jsonschema.NewCompiler()
		compiler.AssertFormat()
		if err := compiler.AddResource(schemaTypesSchemaID, doc); err != nil {
			schemaTypesSchemaErr = fmt.Errorf("catalogpublisher: registering embedded schemaTypes schema: %w", err)
			return
		}
		schema, err := compiler.Compile(schemaTypesSchemaID)
		if err != nil {
			schemaTypesSchemaErr = fmt.Errorf("catalogpublisher: compiling embedded schemaTypes schema: %w", err)
			return
		}
		schemaTypesSchema = schema
	})
	return schemaTypesSchema, schemaTypesSchemaErr
}

// validateSchemaTypes checks schemaTypes against the compiled
// schemaTypesSchema, returning a descriptive error naming catalogID if
// invalid. schemaTypes is optional -- NFH-014 has no "at least one"
// requirement -- so an empty/nil slice is always valid.
func validateSchemaTypes(catalogID string, schemaTypes []string) error {
	if len(schemaTypes) == 0 {
		return nil
	}
	schema, err := compileSchemaTypesSchema()
	if err != nil {
		return fmt.Errorf("publishDirectives entry for catalogId %q: schemaTypes validation unavailable: %w", catalogID, err)
	}
	asAny := make([]any, len(schemaTypes))
	for i, t := range schemaTypes {
		asAny[i] = t
	}
	if err := schema.Validate(asAny); err != nil {
		return fmt.Errorf("publishDirectives entry for catalogId %q: invalid schemaTypes: %w", catalogID, err)
	}
	return nil
}
