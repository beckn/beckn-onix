package handler

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/metric"
	"go.opentelemetry.io/otel/trace"

	"github.com/beckn-one/beckn-onix/pkg/log"
	"github.com/beckn-one/beckn-onix/pkg/model"
	"github.com/beckn-one/beckn-onix/pkg/plugin/definition"
	"github.com/beckn-one/beckn-onix/pkg/telemetry"
)

// signStep represents the signing step in the processing pipeline.
type signStep struct {
	signer       definition.Signer
	km           definition.KeyManager
	payloadStore definition.PayloadStore // may be nil; enables request-signature on solicited callbacks
}

// newSignStep initializes and returns a new signing step.
// payloadStore may be nil; when non-nil and the outgoing action is a solicited
// callback (prefixed with "on_"), signStep looks up the originating request's
// signature and signs with SignAck (4-line signing string, NFH-004 §3.3),
// cryptographically binding the callback to the CN's original request.
func newSignStep(signer definition.Signer, km definition.KeyManager, payloadStore definition.PayloadStore) (definition.Step, error) {
	if signer == nil {
		return nil, fmt.Errorf("invalid config: Signer plugin not configured")
	}
	if km == nil {
		return nil, fmt.Errorf("invalid config: KeyManager plugin not configured")
	}

	return &signStep{signer: signer, km: km, payloadStore: payloadStore}, nil
}

// Run executes the signing step.
func (s *signStep) Run(ctx *model.StepContext) error {
	if len(ctx.SubID) == 0 {
		return model.NewBadReqErr(fmt.Errorf("subscriberID not set"))
	}

	tracer := otel.Tracer(telemetry.ScopeName, trace.WithInstrumentationVersion(telemetry.ScopeVersion))

	var keySet *model.Keyset
	{
		keySetCtx, keySetSpan := tracer.Start(ctx.Context, "keyset")
		ks, err := s.km.Keyset(keySetCtx, ctx.SubID)
		keySetSpan.End()
		if err != nil {
			return fmt.Errorf("failed to get signing key: %w", err)
		}
		keySet = ks
	}

	{
		// Look up the CN's original signature before signing so we can choose
		// Sign (3-line) vs SignAck (4-line) based on whether this is a solicited
		// callback (NFH-004 §3.3).
		requestSig := s.lookupRequestSignature(ctx)

		signerCtx, signerSpan := tracer.Start(ctx.Context, "sign")
		createdAt := time.Now().Unix()
		validTill := time.Now().Add(5 * time.Minute).Unix()
		var sign string
		var err error
		if requestSig != "" {
			sign, err = s.signer.SignAck(signerCtx, ctx.Body, requestSig, keySet.SigningPrivate, createdAt, validTill)
		} else {
			sign, err = s.signer.Sign(signerCtx, ctx.Body, keySet.SigningPrivate, createdAt, validTill)
		}
		signerSpan.End()
		if err != nil {
			return fmt.Errorf("failed to sign request: %w", err)
		}

		authHeader := s.generateAuthHeader(ctx.SubID, keySet.UniqueKeyID, createdAt, validTill, sign, requestSig)
		log.Debugf(ctx, "Signature generated: %v (4-line signing string: %v)", sign, requestSig != "")
		header := model.AuthHeaderSubscriber
		if ctx.Role == model.RoleGateway {
			header = model.AuthHeaderGateway
		}
		ctx.Request.Header.Set(header, authHeader)
	}

	return nil
}

// lookupRequestSignature returns the stored outbound signature for solicited
// callbacks. For a callback action like "on_search" it strips the "on_" prefix
// and looks up the PayloadStore by (messageID, "search").
// Returns empty string when: payloadStore is nil, action is not a callback,
// no entry found, or the store returns an error (non-fatal — we degrade to
// omitting request-signature rather than failing the sign step).
func (s *signStep) lookupRequestSignature(ctx *model.StepContext) string {
	if s.payloadStore == nil {
		return ""
	}
	action := extractBecknAction(ctx.Body)
	if !strings.HasPrefix(action, "on_") {
		return ""
	}
	bareAction := strings.TrimPrefix(action, "on_")
	entry, err := s.payloadStore.GetByMessageID(ctx, ctx.MessageID, bareAction)
	if err != nil {
		log.Warnf(ctx, "signStep: PayloadStore lookup failed for message_id=%s action=%s: %v", ctx.MessageID, bareAction, err)
		return ""
	}
	if entry == nil {
		return ""
	}
	return entry.Signature
}

// generateAuthHeader constructs the Authorization header for the signed request.
// When requestSig is non-empty (solicited callback path) it declares
// "request-signature" in the headers list (NFH-004 §4); the value itself is
// in the signing string, not as a separate header attribute.
func (s *signStep) generateAuthHeader(subID, keyID string, createdAt, validTill int64, signature, requestSig string) string {
	base := fmt.Sprintf(
		"Signature keyId=\"%s|%s|ed25519\",algorithm=\"ed25519\",created=\"%d\",expires=\"%d\"",
		subID, keyID, createdAt, validTill,
	)
	if requestSig != "" {
		return base + fmt.Sprintf(
			",headers=\"(created) (expires) digest request-signature\",signature=\"%s\"",
			signature,
		)
	}
	return base + fmt.Sprintf(",headers=\"(created) (expires) digest\",signature=\"%s\"", signature)
}

// validateSignStep represents the signature validation step.
type validateSignStep struct {
	validator    definition.SignValidator
	km           definition.KeyManager
	metrics      *HandlerMetrics
	payloadStore definition.PayloadStore
}

// newValidateSignStep initializes and returns a new validate sign step.
// payloadStore may be nil; when non-nil and the incoming request is a solicited
// callback (v2, headers declares "request-signature"), ValidateAck is used with
// the stored outbound signature to verify the 4-line signing string (NFH-004 §3.3).
func newValidateSignStep(signValidator definition.SignValidator, km definition.KeyManager, payloadStore definition.PayloadStore) (definition.Step, error) {
	if signValidator == nil {
		return nil, fmt.Errorf("invalid config: SignValidator plugin not configured")
	}
	if km == nil {
		return nil, fmt.Errorf("invalid config: KeyManager plugin not configured")
	}
	metrics, _ := GetHandlerMetrics(context.Background())
	return &validateSignStep{
		validator:    signValidator,
		km:           km,
		metrics:      metrics,
		payloadStore: payloadStore,
	}, nil
}

// Run executes the validation step.
func (s *validateSignStep) Run(ctx *model.StepContext) error {
	tracer := otel.Tracer(telemetry.ScopeName, trace.WithInstrumentationVersion(telemetry.ScopeVersion))
	spanCtx, span := tracer.Start(ctx.Context, "validate-sign")
	defer span.End()
	stepCtx := &model.StepContext{
		Context:         spanCtx,
		Request:         ctx.Request,
		Body:            ctx.Body,
		Role:            ctx.Role,
		SubID:           ctx.SubID,
		RespHeader:      ctx.RespHeader,
		Route:           ctx.Route,
		ProtocolVersion: ctx.ProtocolVersion,
		MessageID:       ctx.MessageID,
	}
	err := s.validateHeaders(stepCtx)
	s.recordMetrics(stepCtx, err)
	return err
}

func (s *validateSignStep) validateHeaders(ctx *model.StepContext) error {
	unauthHeader := fmt.Sprintf("Signature realm=\"%s\",headers=\"(created) (expires) digest\"", ctx.SubID)
	headerValue := ctx.Request.Header.Get(model.AuthHeaderGateway)
	if len(headerValue) != 0 {
		log.Debugf(ctx, "Validating %v Header", model.AuthHeaderGateway)
		if _, err := s.validate(ctx, headerValue, ""); err != nil {
			ctx.RespHeader.Set(model.UnaAuthorizedHeaderGateway, unauthHeader)
			return model.NewSignValidationErr(fmt.Errorf("failed to validate %s: %w", model.AuthHeaderGateway, err))
		}
	}

	log.Debugf(ctx, "Validating %v Header", model.AuthHeaderSubscriber)
	headerValue = ctx.Request.Header.Get(model.AuthHeaderSubscriber)
	if len(headerValue) == 0 {
		ctx.RespHeader.Set(model.UnaAuthorizedHeaderSubscriber, unauthHeader)
		return model.NewSignValidationErr(fmt.Errorf("%s missing", model.UnaAuthorizedHeaderSubscriber))
	}
	reqSig, err := s.lookupCallbackRequestSig(ctx, headerValue)
	if err != nil {
		ctx.RespHeader.Set(model.UnaAuthorizedHeaderSubscriber, unauthHeader)
		return model.NewSignValidationErr(err)
	}
	parsedSubscriberHeader, err := s.validate(ctx, headerValue, reqSig)
	if err != nil {
		ctx.RespHeader.Set(model.UnaAuthorizedHeaderSubscriber, unauthHeader)
		return model.NewSignValidationErr(fmt.Errorf("failed to validate %s: %w", model.AuthHeaderSubscriber, err))
	}
	if err := s.checkSubscriberIdentity(ctx, parsedSubscriberHeader); err != nil {
		ctx.RespHeader.Set(model.UnaAuthorizedHeaderSubscriber, unauthHeader)
		return model.NewSignValidationErr(err)
	}
	return nil
}

// lookupCallbackRequestSig returns the stored CN outbound signature for solicited
// callbacks so that validateHeaders can pass it to ValidateAck (4-line signing
// string, NFH-004 §3.3). Returns "" when any of the following apply, degrading
// to the standard 3-line Validate path:
//   - payloadStore is nil (not configured)
//   - protocol version < 2.0.0
//   - "request-signature" absent from the Authorization headers attribute (provider-initiated)
//   - action cannot be extracted from the body
func (s *validateSignStep) lookupCallbackRequestSig(ctx *model.StepContext, authHeader string) (string, error) {
	if s.payloadStore == nil || !model.IsAtLeastV2(ctx.ProtocolVersion) || !authHeaderIncludesRequestSig(authHeader) {
		return "", nil
	}
	action := strings.TrimPrefix(extractBecknAction(ctx.Body), "on_")
	if action == "" {
		log.Debugf(ctx, "lookupCallbackRequestSig: no on_ action in body; skipping 4-line path")
		return "", nil
	}
	entry, err := s.payloadStore.GetByMessageID(ctx, ctx.MessageID, action)
	if err != nil {
		return "", fmt.Errorf("validateSign: outbound signature lookup failed for message_id=%s action=%s: %w", ctx.MessageID, action, err)
	}
	if entry == nil {
		return "", fmt.Errorf("validateSign: no outbound signature on record for message_id=%s action=%s — callback may be unsolicited or entry expired", ctx.MessageID, action)
	}
	return entry.Signature, nil
}

// validate checks the validity of the provided signature header.
// When requestSig is non-empty (solicited callback path) it calls ValidateAck
// to verify against the 4-line signing string (NFH-004 §3.3); otherwise it
// calls Validate for the standard 3-line signing string.
// Returns the parsed authHeader on success so callers can reuse it without
// re-parsing (e.g. checkSubscriberIdentity).
func (s *validateSignStep) validate(ctx *model.StepContext, value, requestSig string) (*authHeader, error) {
	headerVals, err := parseHeader(value)
	if err != nil {
		return nil, fmt.Errorf("failed to parse header")
	}
	// Guards the keyId component (sub|key|alg); parseAuthHeader in signvalidator
	// guards the outer algorithm= attribute — the two cover different header fields.
	if headerVals.Algorithm != "ed25519" {
		return nil, fmt.Errorf("unsupported algorithm %q: only ed25519 is permitted", headerVals.Algorithm)
	}
	log.Debugf(ctx, "Validating Signature for subscriberID: %v", headerVals.SubscriberID)
	signingPublicKey, _, err := s.km.LookupNPKeys(ctx, headerVals.SubscriberID, headerVals.UniqueID)
	if err != nil {
		return nil, fmt.Errorf("failed to get validation key: %w", err)
	}
	var validErr error
	if requestSig != "" {
		validErr = s.validator.ValidateAck(ctx, ctx.Body, value, requestSig, signingPublicKey)
	} else {
		validErr = s.validator.Validate(ctx, ctx.Body, value, signingPublicKey)
	}
	if validErr != nil {
		return nil, fmt.Errorf("sign validation failed: %w", validErr)
	}
	return headerVals, nil
}

// checkSubscriberIdentity asserts that the subscriber who signed the request
// (from the already-parsed Authorization header) matches the caller identity
// declared in the request body context.
// Uses ContextKeyRemoteID when already resolved by reqpreprocessor; falls back
// to parsing the body directly via model.ResolveCallerID so the check runs even
// without reqpreprocessor in the middleware chain.
func (s *validateSignStep) checkSubscriberIdentity(ctx *model.StepContext, parsed *authHeader) error {
	// Fast path: reqpreprocessor already resolved and cached the caller ID.
	expected, _ := ctx.Value(model.ContextKeyRemoteID).(string)

	// Slow path: reqpreprocessor not configured; parse body directly.
	if expected == "" {
		var payload struct {
			Context map[string]interface{} `json:"context"`
		}
		if err := json.Unmarshal(ctx.Body, &payload); err != nil {
			log.Debugf(ctx, "checkSubscriberIdentity: failed to parse body: %v; skipping check", err)
		} else if payload.Context != nil {
			expected = model.ResolveCallerID(payload.Context, ctx.Role)
		}
	}

	if expected == "" {
		// Gateway role or body has no matching context field;
		// a missing field will be caught by schema validation separately.
		log.Debugf(ctx, "checkSubscriberIdentity: no caller ID available for role %v; skipping check", ctx.Role)
		return nil
	}

	if parsed.SubscriberID != expected {
		return fmt.Errorf("subscriber identity mismatch: signing subscriber %q does not match declared context identity %q",
			parsed.SubscriberID, expected)
	}
	return nil
}

func (s *validateSignStep) recordMetrics(ctx *model.StepContext, err error) {
	if s.metrics == nil {
		return
	}
	status := "success"
	if err != nil {
		status = "failed"
	}
	s.metrics.SignatureValidationsTotal.Add(ctx.Context, 1,
		metric.WithAttributes(telemetry.AttrStatus.String(status)))
}

// authHeaderIncludesRequestSig returns true when the Authorization header's
// headers="..." attribute lists "request-signature" as one of the covered fields.
// This distinguishes solicited callbacks (4-line signing string) from
// provider-initiated pushes (3-line signing string, no request-signature).
func authHeaderIncludesRequestSig(header string) bool {
	const hPrefix = `headers="`
	idx := strings.Index(header, hPrefix)
	if idx == -1 {
		return false
	}
	idx += len(hPrefix)
	end := strings.Index(header[idx:], `"`)
	if end == -1 {
		return false
	}
	return strings.Contains(header[idx:idx+end], "request-signature")
}

// ParsedKeyID holds the components from the parsed Authorization header's keyId.
type authHeader struct {
	SubscriberID string
	UniqueID     string
	Algorithm    string
}

// keyID extracts subscriber_id and unique_key_id from the Authorization header.
// Example keyId format: "{subscriber_id}|{unique_key_id}|{algorithm}"
func parseHeader(header string) (*authHeader, error) {
	// Example: Signature keyId="bpp.example.com|key-1|ed25519",algorithm="ed25519",...
	keyIDPart := ""
	// Look for keyId="<value>"
	const keyIdPrefix = `keyId="`
	startIndex := strings.Index(header, keyIdPrefix)
	if startIndex != -1 {
		startIndex += len(keyIdPrefix)
		endIndex := strings.Index(header[startIndex:], `"`)
		if endIndex != -1 {
			keyIDPart = strings.TrimSpace(header[startIndex : startIndex+endIndex])
		}
	}

	if keyIDPart == "" {
		return nil, fmt.Errorf("keyId parameter not found in Authorization header")
	}

	keyIDComponents := strings.Split(keyIDPart, "|")
	if len(keyIDComponents) != 3 {
		return nil, fmt.Errorf("keyId parameter has incorrect format, expected 3 components separated by '|', got %d for '%s'", len(keyIDComponents), keyIDPart)
	}

	return &authHeader{
		SubscriberID: strings.TrimSpace(keyIDComponents[0]),
		UniqueID:     strings.TrimSpace(keyIDComponents[1]),
		Algorithm:    strings.TrimSpace(keyIDComponents[2]),
	}, nil
}

// stripBasePath returns a shallow copy of u with basePath stripped from the
// front of its Path and the leading slash removed, leaving the clean endpoint
// action (e.g. "search" or "catalog/subscription") in Path.Path.
// Returns nil when u is nil.
func stripBasePath(u *url.URL, basePath string) *url.URL {
	if u == nil {
		return nil
	}
	stripped := *u
	p := strings.TrimPrefix(u.Path, basePath)
	stripped.Path = strings.TrimPrefix(p, "/")
	return &stripped
}

// validateSchemaStep represents the schema validation step.
type validateSchemaStep struct {
	validator definition.SchemaValidator
	basePath  string
	metrics   *HandlerMetrics
}

// newValidateSchemaStep creates and returns the validateSchema step after validation.
func newValidateSchemaStep(schemaValidator definition.SchemaValidator, basePath string) (definition.Step, error) {
	if schemaValidator == nil {
		return nil, fmt.Errorf("invalid config: SchemaValidator plugin not configured")
	}
	log.Debug(context.Background(), "adding schema validator")
	metrics, _ := GetHandlerMetrics(context.Background())
	return &validateSchemaStep{
		validator: schemaValidator,
		basePath:  basePath,
		metrics:   metrics,
	}, nil
}

// Run executes the schema validation step.
func (s *validateSchemaStep) Run(ctx *model.StepContext) error {
	var err error
	if len(ctx.Body) == 0 && ctx.Request.Method == http.MethodPost {
		err = fmt.Errorf("schema validation failed: %w", model.NewBadReqErr(fmt.Errorf("POST request requires a body")))
	} else {
		err = s.validator.Validate(ctx, stripBasePath(ctx.Request.URL, s.basePath), ctx.Body)
		if err != nil {
			err = fmt.Errorf("schema validation failed: %w", err)
		}
	}
	s.recordMetrics(ctx, err)
	return err
}

func (s *validateSchemaStep) recordMetrics(ctx *model.StepContext, err error) {
	if s.metrics == nil {
		return
	}
	status := "success"
	if err != nil {
		status = "failed"
	}
	version := extractProtocolVersion(ctx.Body)
	s.metrics.SchemaValidationsTotal.Add(ctx.Context, 1,
		metric.WithAttributes(
			telemetry.AttrSchemaVersion.String(version),
			telemetry.AttrStatus.String(status),
		))
}

// addRouteStep represents the route determination step.
type addRouteStep struct {
	router   definition.Router
	basePath string
	metrics  *HandlerMetrics
}

// newAddRouteStep creates and returns the addRoute step after validation.
func newAddRouteStep(router definition.Router, basePath string) (definition.Step, error) {
	if router == nil {
		return nil, fmt.Errorf("invalid config: Router plugin not configured")
	}
	metrics, _ := GetHandlerMetrics(context.Background())
	return &addRouteStep{
		router:   router,
		basePath: basePath,
		metrics:  metrics,
	}, nil
}

// Run executes the routing step.
func (s *addRouteStep) Run(ctx *model.StepContext) error {
	route, err := s.router.Route(ctx, stripBasePath(ctx.Request.URL, s.basePath), ctx.Body)
	if err != nil {
		return fmt.Errorf("failed to determine route: %w", err)
	}
	ctx.Route = &model.Route{
		TargetType:  route.TargetType,
		PublisherID: route.PublisherID,
		URL:         route.URL,
	}
	if s.metrics != nil && ctx.Route != nil {
		s.metrics.RoutingDecisionsTotal.Add(ctx.Context, 1,
			metric.WithAttributes(
				telemetry.AttrTargetType.String(ctx.Route.TargetType),
			))
	}
	return nil
}

func extractProtocolVersion(body []byte) string {
	type contextEnvelope struct {
		Context struct {
			Version string `json:"version"`
		} `json:"context"`
	}
	var payload contextEnvelope
	if err := json.Unmarshal(body, &payload); err == nil {
		return payload.Context.Version
	}
	return ""
}

// extractAuthSignature extracts the raw Base64 signature value from a Beckn
// Authorization (or X-Gateway-Authorization) header.
// The header uses the form:
//
//	Signature keyId="...",algorithm="ed25519",...,signature="<base64>"
//
// The returned string is the value inside the signature="..." attribute, or
// empty string if the attribute is absent or malformed.
//
// NOTE: The search uses ",signature=\"" (comma-prefixed) rather than the bare
// "signature=\"" substring so that a non-spec-compliant peer that emits
// "request-signature=..." before "signature=..." in its header does not cause
// the wrong value to be extracted.
func extractAuthSignature(authHeader string) string {
	const marker = `,signature="`
	idx := strings.Index(authHeader, marker)
	if idx < 0 {
		return ""
	}
	rest := authHeader[idx+len(marker):]
	end := strings.IndexByte(rest, '"')
	if end < 0 {
		return ""
	}
	return rest[:end]
}

func extractMessageID(body []byte) string {
	type contextEnvelope struct {
		Context struct {
			MessageID string `json:"messageId"`
		} `json:"context"`
	}
	var payload contextEnvelope
	if err := json.Unmarshal(body, &payload); err == nil {
		return payload.Context.MessageID
	}
	return ""
}

// checkPolicyStep adapts PolicyChecker into the Step interface.
type checkPolicyStep struct {
	checker definition.PolicyChecker
}

func newCheckPolicyStep(policyChecker definition.PolicyChecker) (definition.Step, error) {
	if policyChecker == nil {
		return nil, fmt.Errorf("invalid config: PolicyChecker plugin not configured")
	}
	return &checkPolicyStep{checker: policyChecker}, nil
}

func (s *checkPolicyStep) Run(ctx *model.StepContext) error {
	return s.checker.CheckPolicy(ctx)
}

// storePayloadStep adapts PayloadStore into the Step interface.
type storePayloadStep struct {
	store definition.PayloadStore
}

func newStorePayloadStep(ps definition.PayloadStore) (definition.Step, error) {
	if ps == nil {
		return nil, fmt.Errorf("storePayload: PayloadStore not configured")
	}
	return &storePayloadStep{store: ps}, nil
}

func (s *storePayloadStep) Run(ctx *model.StepContext) error {
	return s.store.Store(ctx)
}
