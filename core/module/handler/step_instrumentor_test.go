package handler

import (
	"context"
	"errors"
	"testing"

	"github.com/beckn-one/beckn-onix/pkg/model"
	"github.com/beckn-one/beckn-onix/pkg/telemetry"
	"github.com/stretchr/testify/require"
)

type stubStep struct {
	err error
}

func (s stubStep) Run(ctx *model.StepContext) error {
	return s.err
}

type mutatingStep struct{}

func (mutatingStep) Run(ctx *model.StepContext) error {
	ctx.Body = []byte(`{"mutated":true}`)
	ctx.Route = nil
	ctx.SubID = "sub-updated"
	ctx.Role = model.RoleBPP
	return nil
}

func TestInstrumentedStepSuccess(t *testing.T) {
	ctx := context.Background()
	provider, err := telemetry.NewTestProvider(ctx)
	require.NoError(t, err)
	defer provider.Shutdown(context.Background())

	step, err := NewInstrumentedStep(stubStep{}, "test-step", "test-module")
	require.NoError(t, err)

	stepCtx := &model.StepContext{
		Context: context.Background(),
		Role:    model.RoleBAP,
	}
	require.NoError(t, step.Run(stepCtx))
}

func TestInstrumentedStepError(t *testing.T) {
	ctx := context.Background()
	provider, err := telemetry.NewTestProvider(ctx)
	require.NoError(t, err)
	defer provider.Shutdown(context.Background())

	step, err := NewInstrumentedStep(stubStep{err: errors.New("boom")}, "test-step", "test-module")
	require.NoError(t, err)

	stepCtx := &model.StepContext{
		Context: context.Background(),
		Role:    model.RoleBAP,
	}
	require.Error(t, step.Run(stepCtx))
}

// ---------------------------------------------------------------------------
// InstrumentedResponseStep tests
// ---------------------------------------------------------------------------

type stubResponseStep struct {
	err     error
	gotRctx *model.ResponseStepContext
}

func (s *stubResponseStep) RunOnResponse(_ *model.StepContext, rctx *model.ResponseStepContext) error {
	s.gotRctx = rctx
	return s.err
}

func TestInstrumentedResponseStepSuccess(t *testing.T) {
	ctx := context.Background()
	provider, err := telemetry.NewTestProvider(ctx)
	require.NoError(t, err)
	defer provider.Shutdown(context.Background())

	step, err := NewInstrumentedResponseStep(&stubResponseStep{}, "signAck", "test-module")
	require.NoError(t, err)

	stepCtx := &model.StepContext{
		Context: context.Background(),
		Role:    model.RoleBAP,
	}
	require.NoError(t, step.RunOnResponse(stepCtx, nil))
}

func TestInstrumentedResponseStepError(t *testing.T) {
	ctx := context.Background()
	provider, err := telemetry.NewTestProvider(ctx)
	require.NoError(t, err)
	defer provider.Shutdown(context.Background())

	step, err := NewInstrumentedResponseStep(&stubResponseStep{err: errors.New("sign failed")}, "signAck", "test-module")
	require.NoError(t, err)

	stepCtx := &model.StepContext{
		Context: context.Background(),
		Role:    model.RoleBAP,
	}
	require.Error(t, step.RunOnResponse(stepCtx, nil))
}

// TestInstrumentedResponseStepPassesRctx verifies that the wrapper forwards
// the *model.ResponseStepContext to the inner step unchanged (URL-routing path).
func TestInstrumentedResponseStepPassesRctx(t *testing.T) {
	ctx := context.Background()
	provider, err := telemetry.NewTestProvider(ctx)
	require.NoError(t, err)
	defer provider.Shutdown(context.Background())

	stub := &stubResponseStep{}
	step, err := NewInstrumentedResponseStep(stub, "validateAckSign", "test-module")
	require.NoError(t, err)

	rctx := &model.ResponseStepContext{StatusCode: 200, Body: []byte(`{"message":{"status":"ACK"}}`)}
	stepCtx := &model.StepContext{
		Context: context.Background(),
		Role:    model.RoleBPP,
	}
	require.NoError(t, step.RunOnResponse(stepCtx, rctx))
	require.Equal(t, rctx, stub.gotRctx)
}
