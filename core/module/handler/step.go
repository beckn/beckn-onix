package handler

import (
	"context"
	"encoding/json"
	"fmt"
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
	signer definition.Signer
	km     definition.KeyManager
}

// newSignStep initializes and returns a new signing step.
func newSignStep(signer definition.Signer, km definition.KeyManager) (definition.Step, error) {
	if signer == nil {
		return nil, fmt.Errorf("invalid config: Signer plugin not configured")
	}
	if km == nil {
		return nil, fmt.Errorf("invalid config: KeyManager plugin not configured")
	}

	return &signStep{signer: signer, km: km}, nil
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
		signerCtx, signerSpan := tracer.Start(ctx.Context, "sign")
		createdAt := time.Now().Unix()
		validTill := time.Now().Add(5 * time.Minute).Unix()
		sign, err := s.signer.Sign(signerCtx, ctx.Body, keySet.SigningPrivate, createdAt, validTill)
		signerSpan.End()
		if err != nil {
			return fmt.Errorf("failed to sign request: %w", err)
		}
		authHeader := s.generateAuthHeader(ctx.SubID, keySet.UniqueKeyID, createdAt, validTill, sign)
		log.Debugf(ctx, "Signature generated: %v", sign)
		header := model.AuthHeaderSubscriber
		if ctx.Role == model.RoleGateway {
			header = model.AuthHeaderGateway
		}
		ctx.Request.Header.Set(header, authHeader)
	}

	return nil
}

// generateAuthHeader constructs the authorization header for the signed request.
// It includes key ID, algorithm, creation time, expiration time, required headers, and signature.
func (s *signStep) generateAuthHeader(subID, keyID string, createdAt, validTill int64, signature string) string {
	return fmt.Sprintf(
		"Signature keyId=\"%s|%s|ed25519\",algorithm=\"ed25519\",created=\"%d\",expires=\"%d\",headers=\"(created) (expires) digest\",signature=\"%s\"",
		subID, keyID, createdAt, validTill, signature,
	)
}

// validateSignStep represents the signature validation step.
type validateSignStep struct {
	validator definition.SignValidator
	km        definition.KeyManager
	metrics   *HandlerMetrics
}

// newValidateSignStep initializes and returns a new validate sign step.
func newValidateSignStep(signValidator definition.SignValidator, km definition.KeyManager) (definition.Step, error) {
	if signValidator == nil {
		return nil, fmt.Errorf("invalid config: SignValidator plugin not configured")
	}
	if km == nil {
		return nil, fmt.Errorf("invalid config: KeyManager plugin not configured")
	}
	metrics, _ := GetHandlerMetrics(context.Background())
	return &validateSignStep{
		validator: signValidator,
		km:        km,
		metrics:   metrics,
	}, nil
}

// Run executes the validation step.
func (s *validateSignStep) Run(ctx *model.StepContext) error {
	tracer := otel.Tracer(telemetry.ScopeName, trace.WithInstrumentationVersion(telemetry.ScopeVersion))
	spanCtx, span := tracer.Start(ctx.Context, "validate-sign")
	defer span.End()
	stepCtx := &model.StepContext{
		Context:    spanCtx,
		Request:    ctx.Request,
		Body:       ctx.Body,
		Role:       ctx.Role,
		SubID:      ctx.SubID,
		RespHeader: ctx.RespHeader,
		Route:      ctx.Route,
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
		if err := s.validate(ctx, headerValue); err != nil {
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
	if err := s.validate(ctx, headerValue); err != nil {
		ctx.RespHeader.Set(model.UnaAuthorizedHeaderSubscriber, unauthHeader)
		return model.NewSignValidationErr(fmt.Errorf("failed to validate %s: %w", model.AuthHeaderSubscriber, err))
	}
	return nil
}

// validate checks the validity of the provided signature header.
func (s *validateSignStep) validate(ctx *model.StepContext, value string) error {
	headerVals, err := parseHeader(value)
	if err != nil {
		return fmt.Errorf("failed to parse header")
	}
	log.Debugf(ctx, "Validating Signature for subscriberID: %v", headerVals.SubscriberID)
	signingPublicKey, _, err := s.km.LookupNPKeys(ctx, headerVals.SubscriberID, headerVals.UniqueID)
	if err != nil {
		return fmt.Errorf("failed to get validation key: %w", err)
	}
	if err := s.validator.Validate(ctx, ctx.Body, value, signingPublicKey); err != nil {
		return fmt.Errorf("sign validation failed: %w", err)
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

// validateSchemaStep represents the schema validation step.
type validateSchemaStep struct {
	validator definition.SchemaValidator
	metrics   *HandlerMetrics
}

// newValidateSchemaStep creates and returns the validateSchema step after validation.
func newValidateSchemaStep(schemaValidator definition.SchemaValidator) (definition.Step, error) {
	if schemaValidator == nil {
		return nil, fmt.Errorf("invalid config: SchemaValidator plugin not configured")
	}
	log.Debug(context.Background(), "adding schema validator")
	metrics, _ := GetHandlerMetrics(context.Background())
	return &validateSchemaStep{
		validator: schemaValidator,
		metrics:   metrics,
	}, nil
}

// Run executes the schema validation step.
func (s *validateSchemaStep) Run(ctx *model.StepContext) error {
	err := s.validator.Validate(ctx, ctx.Request.URL, ctx.Body)
	if err != nil {
		err = fmt.Errorf("schema validation failed: %w", err)
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
	version := extractSchemaVersion(ctx.Body)
	s.metrics.SchemaValidationsTotal.Add(ctx.Context, 1,
		metric.WithAttributes(
			telemetry.AttrSchemaVersion.String(version),
			telemetry.AttrStatus.String(status),
		))
}

// addRouteStep represents the route determination step.
type addRouteStep struct {
	router  definition.Router
	metrics *HandlerMetrics
}

// newAddRouteStep creates and returns the addRoute step after validation.
func newAddRouteStep(router definition.Router) (definition.Step, error) {
	if router == nil {
		return nil, fmt.Errorf("invalid config: Router plugin not configured")
	}
	metrics, _ := GetHandlerMetrics(context.Background())
	return &addRouteStep{
		router:  router,
		metrics: metrics,
	}, nil
}

// Run executes the routing step.
func (s *addRouteStep) Run(ctx *model.StepContext) error {
	route, err := s.router.Route(ctx, ctx.Request.URL, ctx.Body)
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

func extractSchemaVersion(body []byte) string {
	type contextEnvelope struct {
		Context struct {
			Version string `json:"version"`
		} `json:"context"`
	}
	var payload contextEnvelope
	if err := json.Unmarshal(body, &payload); err == nil {
		if payload.Context.Version != "" {
			return payload.Context.Version
		}
	}
	return "unknown"
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
