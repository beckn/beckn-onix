package agrifacility

import "github.com/beckn-one/beckn-onix/pkg/plugin/implementation/internal/upstream"

// prerequisites is what an agriculture facility capability needs that its
// payload does not carry, keyed by binding key.
//
// Empty. A facility search names the point to search from and the facility type
// it wants, and POCRA's search takes exactly those, so there is nothing to
// resolve before the call. The mapping reads both straight off the payload.
//
// An entry would be needed only for real I/O: a district name to resolve to a
// code, a token to exchange, an address to geocode. Each of those is a
// different upstream than the one this was written against, so prefer keeping
// the payload explicit over adding an entry here -- and see the note in
// weather/prerequisites.go before doing so.
var prerequisites = upstream.Prerequisites{}
