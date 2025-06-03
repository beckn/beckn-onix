package model

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"time"
)

// Subscriber represents a unique operational configuration of a trusted platform on a network.
type Subscriber struct {
	SubscriberID string                 `json:"subscriber_id"`
	URL          string                 `json:"url" format:"uri"`
	Type         string                 `json:"type" enum:"BAP,BPP,BG"`
	Domain       string                 `json:"domain"`
	Location     map[string]interface{} `json:"location"`               // Location of the subscriber
	KeyID        string                 `json:"key_id,omitempty"`       // Unique Identifier of the key, if not passed a new key id will be generated
	KeyValidity  int64                  `json:"key_validity,omitempty"` // TTL for key, if not passed a configured value will be used as default
}

// Subscription represents subscription details of a network participant.
type Subscription struct {
	Subscriber       `json:",inline"`
	KeyID            string    `json:"key_id" format:"uuid"`
	SigningPublicKey string    `json:"signing_public_key"`
	EncrPublicKey    string    `json:"encr_public_key"`
	ValidFrom        time.Time `json:"valid_from" format:"date-time"`
	ValidUntil       time.Time `json:"valid_until" format:"date-time"`
	Status           string    `json:"status" enum:"INITIATED,UNDER_SUBSCRIPTION,SUBSCRIBED,EXPIRED,UNSUBSCRIBED,INVALID_SSL"`
	Created          time.Time `json:"created" format:"date-time"`
	Updated          time.Time `json:"updated" format:"date-time"`
	Nonce            string
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
)

var contextKeys = map[string]ContextKey{
	"transaction_id": ContextKeyTxnID,
	"message_id":     ContextKeyMsgID,
	"subscriber_id":  ContextKeySubscriberID,
	"module_id":      ContextKeyModuleID,
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
)

var validRoles = map[Role]bool{
	RoleBAP:       true,
	RoleBPP:       true,
	RoleGateway:   true,
	RoleRegistery: true,
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

// Route represents a network route for message processing.
type Route struct {
	TargetType  string   // "url" or "publisher"
	PublisherID string   // For message queues
	URL         *url.URL // For API calls
}

// Keyset represents a collection of cryptographic keys used for signing and encryption.
type Keyset struct {
	UniqueKeyID    string // UniqueKeyID is the identifier for the key pair.
	SigningPrivate string // SigningPrivate is the private key used for signing operations.
	SigningPublic  string // SigningPublic is the public key corresponding to the signing private key.
	EncrPrivate    string // EncrPrivate is the private key used for encryption operations.
	EncrPublic     string // EncrPublic is the public key corresponding to the encryption private key.
}

// StepContext holds context information for a request processing step.
type StepContext struct {
	context.Context
	Request    *http.Request
	Body       []byte
	Route      *Route
	SubID      string
	Role       Role
	RespHeader http.Header
}

// WithContext updates the existing StepContext with a new context.
func (ctx *StepContext) WithContext(newCtx context.Context) {
	ctx.Context = newCtx
}

// Status represents the acknowledgment status in a response.
type Status string

const (
	// StatusACK indicates a successful acknowledgment.
	StatusACK Status = "ACK"
	// StatusNACK indicates a negative acknowledgment or failure.
	StatusNACK Status = "NACK"
)

// Ack represents an acknowledgment response.
type Ack struct {
	// Status holds the acknowledgment status (ACK/NACK).
	Status Status `json:"status"`
}

// Message represents the structure of a response message.
type Message struct {
	// Ack contains the acknowledgment status.
	Ack Ack `json:"ack"`
	// Error holds error details, if any, in the response.
	Error *Error `json:"error,omitempty"`
}

// Response represents the main response structure.
type Response struct {
	Message Message `json:"message"`
}

// RegistrySubscriptionRequest represents the request sent to registry
type RegistrySubscriptionRequest struct {
	SubscriberID     string                 `json:"subscriber_id"`
	Type             string                 `json:"type"`
	Domain           string                 `json:"domain"`
	Location         map[string]interface{} `json:"location"`
	KeyID            string                 `json:"key_id"`
	URL              string                 `json:"url"`
	SigningPublicKey string                 `json:"signing_public_key"` // Base64-encoded signing public key
	EncrPublicKey    string                 `json:"encr_public_key"`    // Base64-encoded encryption public key
	ValidFrom        string                 `json:"valid_from"`         // Validity start in ISO-8601 format
	ValidUntil       string                 `json:"valid_until"`        // Expiry in ISO-8601 format
	MessageID        string                 `json:"message_id"`         // For correlating subscribe and on_subscribe calls
}
