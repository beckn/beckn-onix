package definition

import (
	"context"
	"errors"
)

// Direction names which half of a mapping to run. A mapping file carries both,
// because the response half usually depends on what the request half did.
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
	Transform(ctx context.Context, mappingRef string, direction Direction, input any) ([]byte, error)
}

// MapperProvider initializes a new Mapper.
type MapperProvider interface {
	New(ctx context.Context, config map[string]string) (Mapper, func() error, error)
}

// ErrNoTransform reports an action a mapping file declares but leaves empty.
//
// An empty mapping is a statement, not an omission: this action needs no
// document built for it, because the caller supplies the request itself. A
// provider taking two query parameters is the ordinary case -- the values are
// already resolved, and passing them through a fetch and a compile to arrive at
// the same two fields buys nothing.
//
// It is a sentinel rather than an empty result so that a caller which does not
// handle it fails loudly. Returning (nil, nil) would let one send an empty
// request instead, which a provider answers with a 200 and the wrong data.
//
// An action absent from the file is a different thing entirely: that capability
// does not serve it, and Transform refuses.
var ErrNoTransform = errors.New("mapping declares this action but supplies no transform")
