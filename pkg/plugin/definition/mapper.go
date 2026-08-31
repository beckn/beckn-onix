package definition

import (
	"context"
	"errors"
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
	// action is the Beckn action of the request being served. The mapping
	// reference must identify itself as being for that action, and Transform
	// refuses if it does not: running a select mapping over a confirm payload
	// would otherwise succeed quietly and produce nonsense.
	Transform(ctx context.Context, mappingRef, action string, input any) ([]byte, error)
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
