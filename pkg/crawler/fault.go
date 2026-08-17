package crawler

// fault.go — the fetch-lifecycle fault taxonomy: PermanentError, the
// FaultClass enum whose Permanent() drives a caller's retry-vs-give-up
// decision, and ClassifyFault which maps an HTTP status + error onto that
// vocabulary.
//
// This package only names classes that arise from fetching, verifying, or
// decoding a remote artifact -- ssrf/oversize/digest/signature/decode/
// content_invalid/transient. A consumer with its own lifecycle (a crawler's
// push-rejected, queue-gap, or storage faults) defines its own additional
// FaultClass constants of this same underlying string type rather than
// this package growing consumer-specific vocabulary; see FaultClass's doc.

import (
	"errors"
	"fmt"
)

// PermanentError marks a failure that won't fix itself on retry -- an
// unsupported encoding, a decompression bomb, a corrupt artifact, an SSRF
// rejection, or a signature/digest mismatch. A caller should give up on
// (park, reject) whatever produced this rather than retry it unchanged.
//
// Class names what actually went wrong, so the raiser (which knows) decides
// the fault label instead of ClassifyFault guessing at the far end. An empty
// Class means "unspecified" and still classifies as FaultDecode.
type PermanentError struct {
	Msg   string
	Class FaultClass
}

func (e *PermanentError) Error() string { return e.Msg }

// Permanentf builds a PermanentError with an unspecified class (=> FaultDecode).
// Prefer PermanentFaultf when the cause has its own class.
func Permanentf(format string, a ...any) error {
	return &PermanentError{Msg: fmt.Sprintf(format, a...)}
}

// PermanentFaultf builds a PermanentError that carries its own FaultClass, so
// a caller sees the real cause (digest_mismatch, ssrf, oversize, signature)
// rather than a blanket "decode" for every permanent failure.
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

// FaultClass is the typed fault taxonomy for a fetch-verify-decode outcome.
// It is a plain string type specifically so a consumer package can define its
// own additional constants of this type for faults outside the fetch
// lifecycle (e.g. a crawler's FaultPushRejected, FaultGap) without needing an
// import cycle back into this package -- only that consumer's own
// permanent-vs-transient decision has to account for the classes it adds;
// this package's own Permanent() only ever answers for the classes it defines
// below.
type FaultClass string

const (
	FaultSSRF           FaultClass = "ssrf"
	FaultOversize       FaultClass = "oversize"
	FaultDigestMismatch FaultClass = "digest_mismatch"
	// FaultSignature is an authenticity failure on a fetched artifact's signed
	// content: no signature, an unknown keyId, a key whose subscription is
	// revoked or expired, or a signature that does not verify. It is
	// deliberately NOT FaultDigestMismatch. A digest mismatch means the bytes
	// disagree with what was declared for them -- a content problem. A
	// signature failure may equally be a key-distribution problem on the
	// registry side, so it needs its own class and its own alert.
	FaultSignature      FaultClass = "signature"
	FaultDecode         FaultClass = "decode"
	FaultContentInvalid FaultClass = "content_invalid"
	FaultTransient      FaultClass = "transient" // generic transient (network/5xx)
)

func (f FaultClass) String() string { return string(f) }

// Permanent reports whether a fault of this class will keep failing on retry.
// Only answers for the classes this package defines; a consumer that adds its
// own FaultClass values must apply its own permanent-vs-transient rule for
// those (this method cannot know about them).
func (f FaultClass) Permanent() bool {
	switch f {
	case FaultSSRF, FaultOversize, FaultDigestMismatch, FaultSignature, FaultDecode, FaultContentInvalid:
		return true
	default: // FaultTransient, or any class this package doesn't define
		return false
	}
}

// ClassifyFault maps an HTTP status + error into a FaultClass, for a caller's
// generic retry-vs-give-up decision: any PermanentError is permanent
// (reporting its own Class, or falling back to FaultDecode if it didn't name
// one); everything else -- including a status the server itself says to
// retry (408 Request Timeout, 425 Too Early, 429 Too Many Requests) -- is
// transient.
func ClassifyFault(httpStatus int, err error) FaultClass {
	if IsPermanent(err) {
		if c := PermanentClass(err); c != "" {
			return c
		}
		return FaultDecode
	}
	return FaultTransient
}
