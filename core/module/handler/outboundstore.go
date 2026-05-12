package handler

import "context"

// outboundAuthStore persists and retrieves the outbound Authorization signature
// for solicited-callback chain verification (NFH-004 §6, issue #679).
//
// # Why a local stub and not definition.PayloadStore
//
// nirmalnr's PayloadStore plugin (issue #707) provides the Redis-backed
// implementation this feature ultimately needs. Until that branch merges we
// cannot import definition.PayloadStore here without creating a circular or
// unavailable dependency. This minimal interface captures only the two
// operations required by #679 so the feature can be reviewed and tested
// independently of #707.
//
// # Merge checklist for #707
//
// Once nirmalnr's branch merges:
//  1. Delete this file.
//  2. Change the outboundStore field on stdHandler and validateSignStep from
//     outboundAuthStore → definition.PayloadStore.
//  3. Update initPlugins to load the PayloadStore plugin for Caller modules.
//  4. Update the ServeHTTP store call to build a definition.PayloadEntry
//     (MessageID, Action, Signature fields are the same; fill the rest from
//     stepCtx: TransactionID, NetworkID, SubscriberID, Role).
//  5. Update validateRequestSignatureChain to call
//     outboundStore.GetByMessageID(ctx, messageID, action) — same signature.
//  6. Remove all TODO(#679/#707) comments once verified.
//
// TODO(#679/#707): replace with definition.PayloadStore after #707 merges.
type outboundAuthStore interface {
	// Store persists the outbound Authorization signature for a sent request.
	// action must be the bare action without the "on_" prefix (e.g. "search").
	Store(ctx context.Context, entry outboundAuthEntry) error

	// GetByMessageID returns the stored entry for the given messageID and bare action.
	// Returns nil, nil (not an error) when the entry is not found or has expired.
	GetByMessageID(ctx context.Context, messageID, action string) (*outboundAuthEntry, error)
}

// outboundAuthEntry records the outbound Authorization signature so that the
// corresponding solicited callback can verify its request-signature field.
//
// The Action is stored without the "on_" prefix (e.g. "search", never "on_search")
// so that Receiver-path lookup — which strips "on_" from the callback action —
// matches the Caller-path store entry.
//
// TODO(#679/#707): replaced by definition.PayloadEntry after #707 merges.
type outboundAuthEntry struct {
	MessageID string // context.messageId from the outbound request
	Action    string // bare action, e.g. "search" (never "on_search")
	Signature string // raw Base64 signature value from the outbound Authorization header
}
