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

// MsgIDKey represents the key for the message ID.
const MsgIDKey = "message_id"

// Role defines different roles in the network.
type Role string

const (
	// RoleBAP represents a Buyer App Participant.
	RoleBAP Role = "bap"

	// RoleBPP represents a Buyer Platform Participant.
	RoleBPP Role = "bpp"

	// RoleGateway represents a Network Gateway.
	RoleGateway Role = "gateway"

	// RoleRegistery represents a Registry Service.
	RoleRegistery Role = "registery"
)

// validRoles ensures only allowed values are accepted
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

// WithContext updates the context in StepContext while keeping other fields unchanged.
func (ctx *StepContext) WithContext(newCtx context.Context) {
	ctx.Context = newCtx // Update the existing context, keeping all other fields unchanged.
}

// Status represents the status of an acknowledgment.
type Status string

const (
	// StatusACK indicates a successful acknowledgment.
	StatusACK Status = "ACK"

	// StatusNACK indicates a negative acknowledgment.
	StatusNACK Status = "NACK"
)

// Ack represents an acknowledgment response.
type Ack struct {
	Status Status `json:"status"` // ACK or NACK
}

// Message represents the message object in the response.
type Message struct {
	Ack   Ack    `json:"ack"`
	Error *Error `json:"error,omitempty"`
}

// Response represents the main response structure.
type Response struct {
	Message Message `json:"message"`
}
