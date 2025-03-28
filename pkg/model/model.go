package model

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"time"
)

type Subscriber struct {
	SubscriberID string `json:"subscriber_id"`
	URL          string `json:"url" format:"uri"`
	Type         string `json:"type" enum:"BAP,BPP,BG"`
	Domain       string `json:"domain"`
}
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

const (
	AuthHeaderSubscriber          string = "Authorization"
	AuthHeaderGateway             string = "X-Gateway-Authorization"
	UnaAuthorizedHeaderSubscriber string = "WWW-Authenticate"
	UnaAuthorizedHeaderGateway    string = "Proxy-Authenticate"
)

type contextKey string

const MsgIDKey = contextKey("message_id")

type Role string

const (
	RoleBAP       Role = "bap"
	RoleBPP       Role = "bpp"
	RoleGateway   Role = "gateway"
	RoleRegistery Role = "registery"
)

var validRoles = map[Role]bool{
	RoleBAP:       true,
	RoleBPP:       true,
	RoleGateway:   true,
	RoleRegistery: true,
}

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

type Route struct {
	Type      string
	URL       *url.URL
	Publisher string
}

type StepContext struct {
	context.Context
	Request    *http.Request
	Body       []byte
	Route      *Route
	SubID      string
	Role       Role
	RespHeader http.Header
}

func (ctx *StepContext) WithContext(newCtx context.Context) {
	ctx.Context = newCtx
}

type Status string

const (
	StatusACK  Status = "ACK"
	StatusNACK Status = "NACK"
)

type Ack struct {
	Status Status `json:"status"`
}
type Message struct {
	Ack   Ack    `json:"ack"`
	Error *Error `json:"error,omitempty"`
}
type Response struct {
	Message Message `json:"message"`
}
