package handler

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/beckn/beckn-onix/pkg/log"
	"github.com/beckn/beckn-onix/pkg/model"
	"github.com/beckn/beckn-onix/pkg/plugin/definition"
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
	keyID, key, err := s.km.SigningPrivateKey(ctx, ctx.SubID)
	if err != nil {
		return fmt.Errorf("failed to get signing key: %w", err)
	}
	createdAt := time.Now().Unix()
	validTill := time.Now().Add(5 * time.Minute).Unix()
	sign, err := s.signer.Sign(ctx, ctx.Body, key, createdAt, validTill)
	if err != nil {
		return fmt.Errorf("failed to sign request: %w", err)
	}
	authHeader := fmt.Sprintf("Signature keyId=\"%s|%s|ed25519\",algorithm=\"ed25519\",created=\"%d\",expires=\"%d\",headers=\"(created) (expires) digest\",signature=\"%s\"", ctx.SubID, keyID, createdAt, validTill, sign)
	header := model.AuthHeaderSubscriber
	if ctx.Role == model.RoleGateway {
		header = model.AuthHeaderGateway
	}
	ctx.Request.Header.Set(header, authHeader)
	return nil
}

// validateSignStep represents the signature validation step.
type validateSignStep struct {
	validator definition.Verifier
	km        definition.KeyManager
}

// newValidateSignStep initializes and returns a new validate sign step.
func newValidateSignStep(signValidator definition.Verifier, km definition.KeyManager) (definition.Step, error) {
	if signValidator == nil {
		return nil, fmt.Errorf("invalid config: SignValidator plugin not configured")
	}
	if km == nil {
		return nil, fmt.Errorf("invalid config: KeyManager plugin not configured")
	}
	return &validateSignStep{validator: signValidator, km: km}, nil
}

// Run executes the validation step.
func (s *validateSignStep) Run(ctx *model.StepContext) error {
	unauthHeader := fmt.Sprintf("Signature realm=\"%s\",headers=\"(created) (expires) digest\"", ctx.SubID)
	headerValue := ctx.Request.Header.Get(model.AuthHeaderGateway)
	if len(headerValue) != 0 {
		if err := s.validate(ctx, headerValue); err != nil {
			ctx.RespHeader.Set(model.UnaAuthorizedHeaderGateway, unauthHeader)
			return model.NewSignValidationErrf("failed to validate %s: %w", model.AuthHeaderGateway, err)
		}
	}
	headerValue = ctx.Request.Header.Get(model.AuthHeaderSubscriber)
	if len(headerValue) == 0 {
		ctx.RespHeader.Set(model.UnaAuthorizedHeaderSubscriber, unauthHeader)
		return model.NewSignValidationErrf("%s missing", model.UnaAuthorizedHeaderSubscriber)
	}
	if err := s.validate(ctx, headerValue); err != nil {
		ctx.RespHeader.Set(model.UnaAuthorizedHeaderSubscriber, unauthHeader)
		return model.NewSignValidationErrf("failed to validate %s: %w", model.AuthHeaderSubscriber, err)
	}
	return nil
}

// validate checks the validity of the provided signature header.
func (s *validateSignStep) validate(ctx *model.StepContext, value string) error {
	headerParts := strings.Split(value, "|")
	ids := strings.Split(headerParts[0], "\"")
	if len(ids) < 2 || len(headerParts) < 3 {
		return fmt.Errorf("malformed sign header")
	}
	subID := ids[1]
	keyID := headerParts[1]
	key, err := s.km.SigningPublicKey(ctx, subID, keyID)
	if err != nil {
		return fmt.Errorf("failed to get validation key: %w", err)
	}
	if _, err := s.validator.Verify(ctx, ctx.Body, []byte(value), key); err != nil {
		return fmt.Errorf("sign validation failed: %w", err)
	}
	return nil
}

// validateSchemaStep represents the schema validation step.
type validateSchemaStep struct {
	validator definition.SchemaValidator
}

// newValidateSchemaStep creates and returns the validateSchema step after validation
func newValidateSchemaStep(schemaValidator definition.SchemaValidator) (definition.Step, error) {
	if schemaValidator == nil {
		return nil, fmt.Errorf("invalid config: SchemaValidator plugin not configured")
	}
	log.Debug(context.Background(), "adding schema validator")
	return &validateSchemaStep{validator: schemaValidator}, nil
}

// Run executes the schema validation step.
func (s *validateSchemaStep) Run(ctx *model.StepContext) error {
	if err := s.validator.Validate(ctx, ctx.Request.URL, ctx.Body); err != nil {
		return fmt.Errorf("schema validation failed: %w", err)
	}
	return nil
}

// addRouteStep represents the route determination step.
type addRouteStep struct {
	router definition.Router
}

// newRouteStep initializes and returns a new routing step.
func newRouteStep(router definition.Router) (definition.Step, error) {
	if router == nil {
		return nil, fmt.Errorf("invalid config: Router plugin not configured")
	}
	return &addRouteStep{router: router}, nil
}

// Run executes the routing step.
func (s *addRouteStep) Run(ctx *model.StepContext) error {
	route, err := s.router.Route(ctx, ctx.Request.URL, ctx.Body)
	if err != nil {
		return fmt.Errorf("failed to determine route: %w", err)
	}
	log.Debugf(ctx, "Routing to %#v", route)
	ctx.Route = route

	log.Debugf(ctx, "ctx.Route to %#v", ctx.Route)
	return nil
}

// broadcastStep is a stub for broadcasting.
type broadcastStep struct{}

// Run is a placeholder for future implementation.
func (b *broadcastStep) Run(ctx *model.StepContext) error {
	// TODO: Implement broadcast logic if needed
	return nil
}
