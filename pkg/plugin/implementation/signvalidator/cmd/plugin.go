package main

import (
	"context"
	"errors"
	"strconv"
	"time"

	"github.com/beckn-one/beckn-onix/pkg/plugin/definition"
	"github.com/beckn-one/beckn-onix/pkg/plugin/implementation/signvalidator"
)

// provider provides instances of Verifier.
type provider struct{}

// New initializes a new Verifier instance.
func (vp provider) New(ctx context.Context, config map[string]string) (definition.SignValidator, func() error, error) {
	if ctx == nil {
		return nil, nil, errors.New("context cannot be nil")
	}

	cfg := &signvalidator.Config{}
	if s, ok := config["clockSkewToleranceSeconds"]; ok {
		secs, err := strconv.Atoi(s)
		if err != nil || secs < 0 {
			return nil, nil, errors.New("signvalidator: clockSkewToleranceSeconds must be a non-negative integer")
		}
		d := time.Duration(secs) * time.Second
		cfg.ClockSkewTolerance = &d
	}

	return signvalidator.New(ctx, cfg)
}

// Provider is the exported symbol that the plugin manager will look for.
var Provider = provider{}
