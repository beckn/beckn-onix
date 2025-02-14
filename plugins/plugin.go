package main

import "context"

type Validator interface {
	Validate(ctx context.Context, b []byte) error //context parameter
}

type ValidatorProvider interface {
	New(p string) (Validator, error)
}
