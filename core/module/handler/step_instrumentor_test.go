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

func TestInstrumentedStep_PropagatesMutations(t *testing.T) {
	ctx := context.Background()
	provider, err := telemetry.NewTestProvider(ctx)
	require.NoError(t, err)
	defer provider.Shutdown(context.Background())

	step, err := NewInstrumentedStep(mutatingStep{}, "test-step", "test-module")
	require.NoError(t, err)

	stepCtx := &model.StepContext{
		Context: context.Background(),
		Body:    []byte(`{"original":true}`),
		Route:   &model.Route{TargetType: "url"},
		SubID:   "sub-initial",
		Role:    model.RoleBAP,
	}
	require.NoError(t, step.Run(stepCtx))

	require.Equal(t, `{"mutated":true}`, string(stepCtx.Body))
	require.Nil(t, stepCtx.Route)
	require.Equal(t, "sub-updated", stepCtx.SubID)
	require.Equal(t, model.RoleBPP, stepCtx.Role)
}
