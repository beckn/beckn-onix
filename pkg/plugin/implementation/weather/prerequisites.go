package weather

import "github.com/beckn-one/beckn-onix/pkg/plugin/implementation/internal/upstream"

// prerequisites is what a weather capability needs that its payload does not
// carry, keyed by binding key.
//
// Empty, and that is the point: every weather capability so far is served by
// reading the payload, which the mapping does. An entry is needed only for real
// I/O -- a station id from a spatial lookup, a session token from an exchange --
// because no expression language should be able to do those.
//
// Adding one is a function and a line here. Nothing else in the package moves,
// and capabilities that need nothing are untouched.
//
// Example, when a provider needs a station id:
//
//	"imd-city|openagrinet:WeatherObservation": resolveStation,
//
// whose result the mapping then reads:
//
//	request: |
//	  { "station": _local.stationId }
var prerequisites = upstream.Prerequisites{}
