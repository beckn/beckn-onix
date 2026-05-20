package main

import (
	"context"

	"github.com/beckn-one/beckn-onix/pkg/plugin/definition"
	"github.com/beckn-one/beckn-onix/pkg/plugin/implementation/reqmapper"
)

type provider struct{}

func (p provider) New(ctx context.Context, c map[string]string) (definition.Step, func(), error) {
	config := &reqmapper.Config{}
	if role, ok := c["role"]; ok {
		config.Role = role
	}
	if mappingsFile, ok := c["mappingsFile"]; ok {
		config.MappingsFile = mappingsFile
	}

	step, err := reqmapper.NewReqMapperStep(config)
	return step, nil, err
}

var Provider = provider{}
