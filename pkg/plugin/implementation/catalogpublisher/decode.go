package catalogpublisher

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"

	"github.com/beckn-one/beckn-onix/core/module/handler"
	"github.com/beckn-one/beckn-onix/pkg/plugin/definition"
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
