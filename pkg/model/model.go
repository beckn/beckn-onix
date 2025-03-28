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
	SubscriberID string `json:"subscriber_id"`
	URL          string `json:"url" format:"uri"`
	Type         string `json:"type" enum:"BAP,BPP,BG"`
	Domain       string `json:"domain"`
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

type contextKey string

// MsgIDKey is the context key used to store and retrieve the message ID in a request context.
const MsgIDKey = contextKey("message_id")

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
	Type      string
	URL       *url.URL
	Publisher string
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
