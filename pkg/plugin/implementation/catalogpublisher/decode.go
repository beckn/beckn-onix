package catalogpublisher

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"

	"github.com/beckn-one/beckn-onix/core/module/handler"
	"github.com/beckn-one/beckn-onix/pkg/plugin/definition"
)

// publishDirective is one entry in publishRequest.Message.PublishDirectives,
// matched to a submitted catalog by CatalogID -- beckn.yaml's own
// CatalogPublishAction.publishDirectives shape (only the fields this
// plugin currently acts on are modeled; catalogType/updateMode/
// resourceDirectives are part of the real schema too but not wired here
// yet). VisibleTo is the spec's name for what the catalog-index file
// itself (and CatalogSubmission) call NetworkIds -- restricts delivery of
// this catalog to the listed network participant ids; omitted means
// visible to all. CatalogType is required by the spec (unlike
// CatalogSubmission.CatalogType, which defaults to "REGULAR" when empty);
// callers must set it explicitly once schemaValidator is configured.
type publishDirective struct {
	CatalogID   string   `json:"catalogId"`
	VisibleTo   []string `json:"visibleTo,omitempty"`
	CatalogType string   `json:"catalogType,omitempty"`
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
// beyond plain JSON decoding: at least one catalog or retire entry must be
// present. Further structural checks (e.g. duplicate catalog ids,
// resourceDirectives referential checks, schemaTypes validation) belong
// here too as this plugin's wire-shape validation grows.
func validatePublishRequest(req publishRequest) error {
	if len(req.Message.Catalogs) == 0 && len(req.Retire) == 0 {
		return fmt.Errorf("message.catalogs or retire is required")
	}
	return nil
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
		})
	}

	return definition.PublishRequest{
		Catalogs:      submissions,
		Retire:        req.Retire,
		ForceBaseline: req.ForceBaseline,
	}, nil
}
