package model

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// Subscriber represents a unique operational configuration of a trusted platform on a network.
type Subscriber struct {
	SubscriberID string `json:"subscriber_id,omitzero"`
	URL          string `json:"url,omitzero" format:"uri"`
	Type         string `json:"type,omitzero" enum:"BAP,BPP,BG"`
	Domain       string `json:"domain,omitzero"`
}

// Subscription represents subscription details of a network participant.
type Subscription struct {
	Subscriber         `json:",inline"`
	KeyID              string    `json:"key_id,omitzero" format:"uuid"`
	SigningPublicKey   string    `json:"signing_public_key,omitzero"`
	EncrPublicKey      string    `json:"encr_public_key,omitzero"`
	ValidFrom          time.Time `json:"valid_from,omitzero" format:"date-time"`
	ValidUntil         time.Time `json:"valid_until,omitzero" format:"date-time"`
	Status             string    `json:"status,omitzero" enum:"INITIATED,UNDER_SUBSCRIPTION,SUBSCRIBED,EXPIRED,UNSUBSCRIBED,INVALID_SSL"`
	Created            time.Time `json:"created,omitzero" format:"date-time"`
	Updated            time.Time `json:"updated,omitzero" format:"date-time"`
	Nonce              string    `json:"nonce,omitzero"`
	NetworkMemberships []string  `json:"network_memberships,omitempty"`
}

// nonUsableKeySubscriptionStatuses are the Subscription.Status values that mean a
// matched subscriber's key is no longer usable for signature verification.
// INITIATED and UNDER_SUBSCRIPTION are intentionally absent — this function
// distinguishes revocation/expiry from good standing only; it does not gate on
// subscription completeness. A subscriber that has registered a key but not yet
// finished onboarding is still treated as usable here. If pre-subscription
// participants should be rejected too, that is a separate authorization
// decision for a caller (or a future ticket) to make, not part of this check.
var nonUsableKeySubscriptionStatuses = map[string]bool{
	"EXPIRED":      true,
	"UNSUBSCRIBED": true,
	"INVALID_SSL":  true,
}

// IsKeyStatusUsable reports whether a subscriber's registry Status still
// permits its key to be used for signature verification.
func IsKeyStatusUsable(status string) bool {
	return !nonUsableKeySubscriptionStatuses[status]
}

// RegistryMetadata represents metadata configured on a registry itself rather than on a specific record.
type RegistryMetadata struct {
	NamespaceIdentifier string
	RegistryName        string
	RawMeta             map[string]string
}

// SubscriberRecord is returned by RegistryMetadataLookup.LookupNode. It carries both the
// subscriber's identity/endpoint data (from the registry details block) and any
// node-level manifest metadata (from the registry meta block) in a single response,
// since both come from the same DeDi endpoint call.
type SubscriberRecord struct {
	Subscription                   // identity, URL, signing/encryption keys — from data["details"]
	Meta         map[string]string // node manifest metadata — from data["meta"]; may be empty
}

// Authorization-related constants for headers.
const (
	AuthHeaderSubscriber          string = "Authorization"
	AuthHeaderGateway             string = "X-Gateway-Authorization"
	UnaAuthorizedHeaderSubscriber string = "WWW-Authenticate"
	UnaAuthorizedHeaderGateway    string = "Proxy-Authenticate"
)

// ContextKey is a custom type used as a key for storing and retrieving values in a context.
type ContextKey string

const (
	// ContextKeyTxnID is the context key used to store and retrieve the transaction ID in a request context.
	ContextKeyTxnID ContextKey = "transaction_id"

	// ContextKeyMsgID is the context key used to store and retrieve the message ID in a request context.
	ContextKeyMsgID ContextKey = "message_id"

	// ContextKeySubscriberID is the context key used to store and retrieve the subscriber ID in a request context.
	ContextKeySubscriberID ContextKey = "subscriber_id"

	// ContextKeyModuleID is the context key for storing and retrieving the model ID from a request context.
	ContextKeyModuleID ContextKey = "module_id"

	// ContextKeyParentID is the context key for  storing  and retrieving the parent ID from a request context
	ContextKeyParentID ContextKey = "parent_id"

	// ContextKeyRemoteID is the context key for the caller who is calling the bap/bpp
	ContextKeyRemoteID ContextKey = "remote_id"

	// ContextKeyProtocolVersion is the context key for the Beckn protocol version
	// extracted from context.version in the inbound request body.
	ContextKeyProtocolVersion ContextKey = "protocol_version"

	// ContextKeyNetworkID is the context key for the network identifier extracted from
	// context.network_id (or context.networkId) in the inbound request body.
	ContextKeyNetworkID ContextKey = "network_id"
)

// ProtocolVersionV2 is the Beckn protocol version string for the v2.0.0 release.
// Steps and response functions gate v2+ behaviour on this value.
const ProtocolVersionV2 = "2.0.0"

// IsAtLeastV2 reports whether the given protocol version string is 2.0.0 or later.
// The check is intentionally major-version based: any version with major >= 2
// (e.g. "2.1.0", "3.0.0") is treated as v2-compatible, while legacy
// 1.x versions and empty/unknown strings return false.
func IsAtLeastV2(version string) bool {
	if version == "" {
		return false
	}
	major, err := strconv.Atoi(strings.SplitN(version, ".", 2)[0])
	if err != nil {
		return false
	}
	return major >= 2
}

var contextKeys = map[string]ContextKey{
	// snake_case keys (legacy beckn spec)
	"transaction_id": ContextKeyTxnID,
	"message_id":     ContextKeyMsgID,
	"subscriber_id":  ContextKeySubscriberID,
	"module_id":      ContextKeyModuleID,
	"parent_id":      ContextKeyParentID,
	"remote_id":      ContextKeyRemoteID,
	// camelCase aliases (new beckn spec — map to the same internal constants)
	"transactionId": ContextKeyTxnID,
	"messageId":     ContextKeyMsgID,
}

// ParseContextKey converts a string into a valid ContextKey.
func ParseContextKey(v string) (ContextKey, error) {
	key, ok := contextKeys[v]
	if !ok {
		return "", fmt.Errorf("invalid context key: %s", v)
	}
	return key, nil
}

// UnmarshalYAML ensures that only known context keys are accepted during YAML unmarshalling.
func (k *ContextKey) UnmarshalYAML(unmarshal func(interface{}) error) error {
	var keyStr string
	if err := unmarshal(&keyStr); err != nil {
		return err
	}

	parsedKey, err := ParseContextKey(keyStr)
	if err != nil {
		return err
	}

	*k = parsedKey
	return nil

}

// Role defines the type of participant in the network.
type Role string

const (
	// RoleBAP represents a Buyer App Participant (BAP) in the network.
	RoleBAP Role = "bap"
	// RoleBPP represents a Buyer Platform Participant (BPP) in the network.
	RoleBPP Role = "bpp"
	// RoleGateway represents a Gateway that facilitates communication in the network.
	RoleGateway Role = "gateway"
	// RoleRegistery represents the Registry that maintains network participant details.
	RoleRegistery Role = "registery"
	// RoleDiscovery represents the discovery for that network
	RoleDiscovery Role = "discovery"
)

var validRoles = map[Role]bool{
	RoleBAP:       true,
	RoleBPP:       true,
	RoleGateway:   true,
	RoleRegistery: true,
	RoleDiscovery: true,
}

// UnmarshalYAML implements custom YAML unmarshalling for Role to ensure only valid values are accepted.
func (r *Role) UnmarshalYAML(unmarshal func(interface{}) error) error {
	var roleName string
	if err := unmarshal(&roleName); err != nil {
		return err
	}

	role := Role(roleName)
	if !validRoles[role] {
		return fmt.Errorf("invalid Role: %s", roleName)
	}
	*r = role
	return nil
}

// ExtractContext decodes body as JSON and returns both the full decoded body
// and its "context" field as a map[string]interface{}, classifying the
// failure onto the Beckn v2.0.0 ErrorCode taxonomy (SCH_INVALID_JSON if body
// isn't valid JSON, SCH_REQUIRED_FIELD_MISSING if "context" is missing or not
// an object) so callers can reuse the same Code/Message regardless of how
// each formats its own response (e.g. wrapping in NewCodedBadReqErr, or
// writing a bare JSON body directly). req and reqContext are nil on failure.
// becknErr wraps the underlying json.Unmarshal error for the SCH_INVALID_JSON
// case (via Error.Unwrap; nil for the missing-context case, which has no
// cause of its own) — callers that want errors.Is/errors.As to keep reaching
// it can call errors.As/errors.Is on becknErr directly.
func ExtractContext(body []byte) (req map[string]interface{}, reqContext map[string]interface{}, becknErr *Error) {
	if err := json.Unmarshal(body, &req); err != nil {
		return nil, nil, NewCodedErrorWithCause("SCH_INVALID_JSON", fmt.Sprintf("failed to decode request body: %v", err), "", err)
	}
	reqContext, ok := req["context"].(map[string]interface{})
	if !ok {
		return nil, nil, NewCodedError("SCH_REQUIRED_FIELD_MISSING", "context field not found or invalid")
	}
	return req, reqContext, nil
}

// WrapExtractContextErr converts an ExtractContext failure into a *BadReqErr
// carrying becknErr's Code, for callers that need an error value rather than
// the bare *Error ExtractContext returns. becknErr is wrapped with prefix via
// %w, so errors.Is/errors.As still reach any cause set on it (via
// Error.Unwrap) as well as becknErr itself. Only call this when becknErr is
// non-nil.
func WrapExtractContextErr(prefix string, becknErr *Error) *BadReqErr {
	return NewCodedBadReqErr(becknErr.Code, fmt.Errorf("%s: %w", prefix, becknErr))
}

// ResolveNetworkID returns context.network_id from a parsed Beckn context map,
// trying "network_id" (snake_case) then "networkId" (camelCase).
// Returns "" when absent, empty, or not a string under either alias.
func ResolveNetworkID(reqContext map[string]interface{}) string {
	for _, k := range []string{"network_id", "networkId"} {
		if v, ok := reqContext[k].(string); ok && v != "" {
			return v
		}
	}
	return ""
}

// ResolveCallerID returns the ID of the other party in a Beckn exchange — i.e. who
// sent the inbound message — from a parsed Beckn context map.
// For BPP role: the caller is the BAP (bap_id / bapId / senderId).
// For BAP role: the caller is the BPP (bpp_id / bppId / receiverId).
// Returns "" when the role is not BAP/BPP or no matching key is found.
func ResolveCallerID(reqContext map[string]interface{}, role Role) string {
	var keys []string
	switch role {
	case RoleBAP:
		keys = []string{"bpp_id", "bppId", "receiverId"}
	case RoleBPP:
		keys = []string{"bap_id", "bapId", "senderId"}
	default:
		return ""
	}
	for _, k := range keys {
		if v, ok := reqContext[k].(string); ok && v != "" {
			return v
		}
	}
	return ""
}

// ResolveSubscriberID returns the node's own subscriber ID from a parsed Beckn context map.
// For BAP role: returns bap_id / bapId / senderId.
// For BPP role: returns bpp_id / bppId / receiverId.
// Returns "" when the role is not BAP/BPP or no matching key is found.
func ResolveSubscriberID(reqContext map[string]interface{}, role Role) string {
	var keys []string
	switch role {
	case RoleBAP:
		keys = []string{"bap_id", "bapId", "senderId"}
	case RoleBPP:
		keys = []string{"bpp_id", "bppId", "receiverId"}
	default:
		return ""
	}
	for _, k := range keys {
		if v, ok := reqContext[k].(string); ok && v != "" {
			return v
		}
	}
	return ""
}

// Route represents a network route for message processing.
type Route struct {
	TargetType  string   // "url" or "publisher"
	PublisherID string   // For message queues
	URL         *url.URL // For API calls
}

// Keyset represents a collection of cryptographic keys used for signing and encryption.
type Keyset struct {
	SubscriberID   string
	UniqueKeyID    string // UniqueKeyID is the identifier for the key pair.
	SigningPrivate string // SigningPrivate is the private key used for signing operations.
	SigningPublic  string // SigningPublic is the public key corresponding to the signing private key.
	EncrPrivate    string // EncrPrivate is the private key used for encryption operations.
	EncrPublic     string // EncrPublic is the public key corresponding to the encryption private key.
}

// StepContext holds context information for a request processing step.
type StepContext struct {
	context.Context
	Request              *http.Request
	Body                 []byte
	Route                *Route
	SubID                string
	Role                 Role
	RespHeader           http.Header
	ProtocolVersion      string // Protocol version parsed from context.version (e.g. "2.0.0")
	MessageID            string // Message ID parsed from context.messageId in the request body
	InboundAuthSignature string // Raw Base64 signature from the inbound Authorization header's signature="..." attribute
	IsCallerHandler      bool   // True when the handler is a Caller (outbound); false for Receiver (inbound)
}

// WithContext updates the existing StepContext with a new context.
func (ctx *StepContext) WithContext(newCtx context.Context) {
	ctx.Context = newCtx
}

// ResponseStepContext carries response-phase data for the response step pipeline.
// It is constructed by the handler from *http.Response before response steps run,
// keeping transport types out of the ResponseStep interface.
//
// A nil ResponseStepContext signals the publisher path — ONIX writes the ACK
// itself and there is no upstream response to inspect.
//
// Header is a shared reference to resp.Header; mutations made by steps (e.g.
// writing the Signature header) are visible to the handler and forwarded by
// ReverseProxy without any explicit write-back.
type ResponseStepContext struct {
	StatusCode int
	Header     http.Header // shared reference — step mutations visible to caller
	Body       []byte      // pre-read response body; nil on publisher path
}

// Status represents the acknowledgment status in a response.
type Status string

const (
	// StatusACK indicates a successful acknowledgment.
	StatusACK Status = "ACK"
	// StatusNACK indicates a negative acknowledgment or failure.
	StatusNACK Status = "NACK"
)

// Message represents the synchronous response message envelope (Beckn v2.0.0 LTS shape).
// The status and messageId are direct fields; the legacy "ack" wrapper is gone.
// For wire format: {"message":{"status":"ACK","messageId":"<uuid>"}}.
type Message struct {
	// Status holds the acknowledgment status (ACK/NACK).
	Status Status `json:"status"`
	// MessageID echoes the context.messageId from the inbound request.
	MessageID string `json:"messageId,omitempty"`
	// Error holds error details when Status is NACK.
	Error *Error `json:"error,omitempty"`
}

// Response represents the main response structure.
type Response struct {
	Message Message `json:"message"`
}
