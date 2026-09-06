package main

import (
	"context"

	"github.com/beckn-one/beckn-onix/pkg/plugin/definition"
	"github.com/beckn-one/beckn-onix/pkg/plugin/implementation/reqmapper"
)

type provider struct{}

func (p provider) New(ctx context.Context, translator definition.Translator, c map[string]string) (definition.Step, func(), error) {
	step, err := reqmapper.NewReqMapperStep(reqmapper.BuildConfig(c), translator)
	return step, nil, err
}

// Provider is the exported symbol that the beckn-onix plugin manager looks up.
var Provider = provider{}
