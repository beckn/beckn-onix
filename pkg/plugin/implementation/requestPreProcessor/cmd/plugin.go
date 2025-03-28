package main

import (
	"context"
	"net/http"
	"strings"

	requestpreprocessor "github.com/beckn/beckn-onix/pkg/plugin/implementation/requestPreProcessor"
)

type provider struct{}

func (p provider) New(ctx context.Context, c map[string]string) (func(http.Handler) http.Handler, error) {
	config := &requestpreprocessor.Config{}
	if contextKeysStr, ok := c["ContextKeys"]; ok {
		config.ContextKeys = strings.Split(contextKeysStr, ",")
	}
	return requestpreprocessor.NewUUIDSetter(config)
}

var Provider = provider{}
