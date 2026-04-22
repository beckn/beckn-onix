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
	err        error
	gotVersion string
}

func (s *stubStep) Run(ctx *model.StepContext) error {
	s.gotVersion = ctx.BecknVersion
	return s.err
}

func TestInstrumentedStepSuccess(t *testing.T) {
	ctx := context.Background()
	provider, err := telemetry.NewTestProvider(ctx)
	require.NoError(t, err)
	defer provider.Shutdown(context.Background())

	stub := &stubStep{}
	step, err := NewInstrumentedStep(stub, "test-step", "test-module")
	require.NoError(t, err)

	stepCtx := &model.StepContext{
		Context:      context.Background(),
		Role:         model.RoleBAP,
		BecknVersion: "2.0.0",
	}
	require.NoError(t, step.Run(stepCtx))
	require.Equal(t, "2.0.0", stub.gotVersion)
}

func TestInstrumentedStepError(t *testing.T) {
	ctx := context.Background()
	provider, err := telemetry.NewTestProvider(ctx)
	require.NoError(t, err)
	defer provider.Shutdown(context.Background())

	stub := &stubStep{err: errors.New("boom")}
	step, err := NewInstrumentedStep(stub, "test-step", "test-module")
	require.NoError(t, err)

	stepCtx := &model.StepContext{
		Context:      context.Background(),
		Role:         model.RoleBAP,
		BecknVersion: "2.0.0-rc",
	}
	require.Error(t, step.Run(stepCtx))
	require.Equal(t, "2.0.0-rc", stub.gotVersion)
}
