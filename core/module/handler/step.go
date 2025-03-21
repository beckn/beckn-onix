package handler

import (
	"fmt"

	"github.com/beckn/beckn-onix/plugin/definition"
)

// SignStep represents the signing step in the process.
type SignStep struct {
	Signer definition.Signer
}

// Run executes the signing step by signing the request body.
func (s *SignStep) Run(ctx *definition.StepContext) error {
	sign, err := s.Signer.Sign(ctx, ctx.Body, ctx.SigningKey)
	if err != nil {
		return fmt.Errorf("failed to sign request: %w", err)
	}
	ctx.Request.Header.Set("SignHeader", sign)
	return nil
}

// validateSignStep represents the step to validate a signature.
type validateSignStep struct {
	validator definition.SignValidator
}

// Run executes the signature validation step.
func (s *validateSignStep) Run(ctx *definition.StepContext) error {
	headerValue := ctx.Request.Header.Get("HeaderString")
	valid, err := s.validator.Verify(ctx, ctx.Body, headerValue, "key")
	if err != nil {
		return fmt.Errorf("sign validation failed: %w", err)
	}
	if !valid {
		return fmt.Errorf("sign validation failed: signature is invalid")
	}
	return nil
}

// validateSchemaStep represents the step to validate a schema.
type validateSchemaStep struct {
	validator definition.SchemaValidator
}

// Run executes the schema validation step.
func (s *validateSchemaStep) Run(ctx *definition.StepContext) error {
	if err := s.validator.Validate(ctx, ctx.Request.URL, ctx.Body); err != nil {
		return fmt.Errorf("schema validation failed: %w", err)
	}
	return nil
}

// addRouteStep represents the step to determine and add a route.
type addRouteStep struct {
	router definition.Router
}

// Run executes the route determination step.
func (s *addRouteStep) Run(ctx *definition.StepContext) error {
	route, err := s.router.Route(ctx, ctx.Request.URL, ctx.Body)
	if err != nil {
		return fmt.Errorf("failed to determine route: %w", err)
	}
	ctx.Route = route
	return nil
}

// broadcastStep is a stub for future broadcast implementation.
type broadcastStep struct{}

// Run executes the broadcast step (currently unimplemented).
func (b *broadcastStep) Run(ctx *definition.StepContext) error {
	// TODO: Implement broadcast logic if needed
	return nil
}

// subscribeStep is a stub for future subscription implementation.
type subscribeStep struct{}

// Run executes the subscription step (currently unimplemented).
func (s *subscribeStep) Run(ctx *definition.StepContext) error {
	// TODO: Implement subscription logic if needed
	return nil
}
