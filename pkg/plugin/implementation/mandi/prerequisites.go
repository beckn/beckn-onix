package mandi

import "github.com/beckn-one/beckn-onix/pkg/plugin/implementation/internal/upstream"

// prerequisites is what a mandi capability needs that its payload does not
// carry, keyed by binding key.
//
// Empty, and for a better reason than weather's: a market price select names
// the market it wants. The MandiPrice pack has no top-level location -- only
// market.marketCode, market.district and market.state -- so there is nothing
// to resolve. Agmarknet's Vistaar select takes exactly those codes, and the
// mapping reads them straight off the payload.
//
// An entry would be needed only for real I/O: a commodity name to resolve to a
// code, a token to exchange, a point to turn into a market. Each of those is a
// different upstream than the one this was written against, and each would
// bring the question of where the provider-to-function binding belongs -- see
// the note in weather/prerequisites.go and prefer keeping the payload explicit
// over adding an entry here.
var prerequisites = upstream.Prerequisites{}
