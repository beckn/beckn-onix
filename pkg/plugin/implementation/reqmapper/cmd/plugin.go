package main

import (
	"context"

	"github.com/beckn-one/beckn-onix/pkg/plugin/definition"
	"github.com/beckn-one/beckn-onix/pkg/plugin/implementation/reqmapper"
)

type provider struct{}

func (p provider) New(ctx context.Context, c map[string]string) (definition.Step, func(), error) {
	step, err := reqmapper.NewReqMapperStep(reqmapper.BuildConfig(c))
	return step, nil, err
}

var Provider = provider{}
