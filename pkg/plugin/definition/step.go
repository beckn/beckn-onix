package definition

import (
	"context"

	"github.com/beckn-one/beckn-onix/pkg/model"
)

type Step interface {
	Run(ctx *model.StepContext) error
}

type StepProvider interface {
	New(context.Context, map[string]string) (Step, func(), error)
}
