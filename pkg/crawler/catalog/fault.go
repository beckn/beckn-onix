package catalog

// fault.go — fault taxonomy: PermanentError, the FaultClass enum whose
// Permanent() drives the park-vs-retry decision, and ClassifyFault which maps
// an HTTP status + error onto that vocabulary.

import (
	"errors"
	"fmt"
)

// PermanentError marks a failure that won't fix itself on retry — an
// unsupported encoding, a decompression bomb, a corrupt artifact, or a
// continuity gap. The runner parks these (no hot retry) and never advances the
// version cursor.
type PermanentError struct{ Msg string }

func (e *PermanentError) Error() string { return e.Msg }

// Permanentf builds a PermanentError. Exported so adapter packages (decode,
// fetch) can raise permanent faults over the shared taxonomy.
func Permanentf(format string, a ...any) error {
	return &PermanentError{Msg: fmt.Sprintf(format, a...)}
}

// IsPermanent reports whether err (or a wrapped cause) is a PermanentError.
func IsPermanent(err error) bool {
	var pe *PermanentError
	return errors.As(err, &pe)
}

// FaultClass is the typed fault taxonomy (§6b): one vocabulary for what went
// wrong, whose Permanent() drives the park-vs-retry decision. (In PR-B it also
// feeds telemetry; for now the metric label still comes from reasonCategory.)
type FaultClass string

const (
	FaultSSRF           FaultClass = "ssrf"
	FaultOversize       FaultClass = "oversize"
	FaultDigestMismatch FaultClass = "digest_mismatch"
	FaultDecode         FaultClass = "decode"
	FaultContentInvalid FaultClass = "content_invalid"
	FaultPushSchema     FaultClass = "push_schema"
	FaultPushRejected   FaultClass = "push_rejected"
	FaultGap            FaultClass = "gap"
	FaultAbsent         FaultClass = "absent"
	FaultStore          FaultClass = "store"
	FaultIndexFetch     FaultClass = "index_fetch"
	FaultTransient      FaultClass = "transient" // generic transient (network/5xx)
)

func (f FaultClass) String() string { return string(f) }

// Permanent reports whether a fault of this class will keep failing on retry
// (so it is parked, not retried). Transient faults (network, 5xx, generic) are
// retried.
func (f FaultClass) Permanent() bool {
	switch f {
	case FaultSSRF, FaultOversize, FaultDigestMismatch, FaultDecode,
		FaultContentInvalid, FaultPushSchema, FaultPushRejected, FaultGap, FaultAbsent:
		return true
	default: // FaultIndexFetch, FaultTransient, FaultStore
		return false
	}
}

// ClassifyFault maps an HTTP status + error into a FaultClass, reproducing the
// crawler's park-vs-retry rule: a 4xx push rejection or any PermanentError is
// permanent; everything else is transient.
//
// Exception: 408 (Request Timeout), 425 (Too Early) and 429 (Too Many Requests)
// are 4xx statuses the server itself says to retry — parking them permanently
// would strand a catalog on a transient rate-limit/timeout. They classify as
// transient so the backoff/retry path handles them.
func ClassifyFault(httpStatus int, err error) FaultClass {
	switch {
	case httpStatus == 408 || httpStatus == 425 || httpStatus == 429:
		return FaultTransient
	case httpStatus >= 400 && httpStatus < 500:
		return FaultPushRejected
	case IsPermanent(err):
		return FaultDecode
	default:
		return FaultTransient
	}
}
