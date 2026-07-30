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
//
// Class names what actually went wrong, so the raiser (which knows) decides the
// fault label instead of ClassifyFault guessing at the far end. An empty Class
// means "unspecified" and still classifies as FaultDecode.
type PermanentError struct {
	Msg   string
	Class FaultClass
}

func (e *PermanentError) Error() string { return e.Msg }

// Permanentf builds a PermanentError with an unspecified class (=> FaultDecode).
// Exported so adapter packages (decode, fetch) can raise permanent faults over
// the shared taxonomy. Prefer PermanentFaultf when the cause has its own class.
func Permanentf(format string, a ...any) error {
	return &PermanentError{Msg: fmt.Sprintf(format, a...)}
}

// PermanentFaultf builds a PermanentError that carries its own FaultClass, so an
// operator sees the real cause (digest_mismatch, ssrf, oversize, gap) rather
// than a blanket "decode" for every permanent failure.
func PermanentFaultf(class FaultClass, format string, a ...any) error {
	return &PermanentError{Msg: fmt.Sprintf(format, a...), Class: class}
}

// IsPermanent reports whether err (or a wrapped cause) is a PermanentError.
func IsPermanent(err error) bool {
	var pe *PermanentError
	return errors.As(err, &pe)
}

// PermanentClass returns the FaultClass a PermanentError carries, or "" if err
// is not permanent or did not name one.
func PermanentClass(err error) FaultClass {
	var pe *PermanentError
	if errors.As(err, &pe) {
		return pe.Class
	}
	return ""
}

// FaultClass is the typed fault taxonomy (§6b): one vocabulary for what went
// wrong, whose Permanent() drives the park-vs-retry decision. (In PR-B it also
// feeds telemetry; for now the metric label still comes from reasonCategory.)
type FaultClass string

const (
	FaultSSRF           FaultClass = "ssrf"
	FaultOversize       FaultClass = "oversize"
	FaultDigestMismatch FaultClass = "digest_mismatch"
	// FaultSignature is an authenticity failure on a catalog file's signed
	// tuple: no signature, an expired one, a keyId the registry does not know,
	// a key whose subscription is revoked or expired, or a signature that does
	// not verify. It is deliberately NOT digest_mismatch. A digest mismatch
	// means the publisher's bytes do not match what the publisher declared, so
	// the operator table routes it to "contact the publisher". A signature
	// failure may equally be a key-distribution problem on the registry side,
	// so it needs its own row in that table and its own alert.
	FaultSignature      FaultClass = "signature"
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
//
// FaultSignature is permanent. Every failure raised under it is a definitive
// verdict about the file itself or about the key the registry actually holds:
// re-fetching the same bytes yields the same unsigned, expired, forged or
// unknown-key entry. The one signature-path failure that is NOT definitive, a
// registry that could not be reached, is deliberately never raised as
// FaultSignature; it stays an unclassified error so ClassifyFault reports
// FaultTransient and the runner retries it. See fetch/verify.go.
func (f FaultClass) Permanent() bool {
	switch f {
	case FaultSSRF, FaultOversize, FaultDigestMismatch, FaultSignature, FaultDecode,
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
// A PermanentError that named its own Class reports that class; one that didn't
// falls back to FaultDecode. Without the class, every permanent failure — a
// continuity gap, a tampered digest, an SSRF rejection — surfaced as
// fault_class="decode" and pointed operators at the wrong cause.
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
		if c := PermanentClass(err); c != "" {
			return c
		}
		return FaultDecode
	default:
		return FaultTransient
	}
}
