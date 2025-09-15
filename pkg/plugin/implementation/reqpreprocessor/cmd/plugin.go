package main

import (
	"context"
	"net/http"
	"strings"

	"github.com/beckn-one/beckn-onix/pkg/plugin/implementation/reqpreprocessor"
)

type provider struct{}

func (p provider) New(ctx context.Context, c map[string]string) (func(http.Handler) http.Handler, error) {
	config := &reqpreprocessor.Config{}
	if role, ok := c["role"]; ok {
		config.Role = role
	}
	if contextKeys, ok := c["contextKeys"]; ok {
		config.ContextKeys = strings.Split(contextKeys, ",")
	}
	return reqpreprocessor.NewPreProcessor(config)
}

var Provider = provider{}
