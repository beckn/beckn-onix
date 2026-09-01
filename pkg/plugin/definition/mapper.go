package definition

import (
	"context"
)

// Direction names which half of a mapping to run. A mapping file carries both,
// because both legs of one upstream call belong together.
type Direction string

const (
	// DirectionRequest translates an inbound payload into what the upstream wants.
	DirectionRequest Direction = "request"
	// DirectionResponse translates the upstream's answer back.
	DirectionResponse Direction = "response"
)

// Mapper transforms a document with a mapping fetched from a reference.
//
// It exists so that translating between OAN's Beckn payloads and a provider's
// own shape is configuration rather than code: a new provider ships mapping
// files, not a new transformation routine. The mapper itself knows nothing
// about any provider, and nothing about what a mapping says -- it fetches,
// compiles and runs whatever the reference points at.
type Mapper interface {
	// Transform runs the mapping at mappingRef over input and returns the
	// result.
	//
	// mappingRef is what the registry carries verbatim: the URL of one published
	// file holding both directions.
	//
	// Which action the mapping serves is settled by the registry entry that
	// named it, so only the direction is passed here.
	//
	// input carries what a party sent: the inbound payload, and on the way back
	// the provider's answer. It deliberately does not carry values the caller
	// resolved for itself -- the caller holds those already, so routing them
	// through a mapping would be a detour and a second name for the same data.
	//
	// A direction the file has no transform for produces nothing, with no error.
	// What nothing means belongs to the caller: on the request leg it means there
	// is no document to send.
	Transform(ctx context.Context, mappingRef string, direction Direction, input any) ([]byte, error)

	// Verify checks the preconditions the mapping at mappingRef declares, and
	// returns an error carrying the mapping's own explanation when one fails.
	//
	// It exists because a mapping otherwise cannot refuse. Without it, every
	// judgement about whether a payload can be served at all lives in Go, so a
	// provider with its own rule needs its own build -- and the rule and the
	// extraction it guards end up in different places.
	//
	// A mapping declaring no preconditions imposes none. That is what lets the
	// facility be adopted per provider rather than all at once.
	Verify(ctx context.Context, mappingRef string, input any) error
}

// MapperProvider initializes a new Mapper.
type MapperProvider interface {
	New(ctx context.Context, config map[string]string) (Mapper, func() error, error)
}
