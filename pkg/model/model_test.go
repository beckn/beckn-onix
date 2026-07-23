package model

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"
)

func TestIsKeyStatusUsable(t *testing.T) {
	tests := []struct {
		status string
		want   bool
	}{
		{status: "SUBSCRIBED", want: true},
		{status: "INITIATED", want: true},
		{status: "UNDER_SUBSCRIPTION", want: true},
		{status: "", want: true},
		{status: "EXPIRED", want: false},
		{status: "UNSUBSCRIBED", want: false},
		{status: "INVALID_SSL", want: false},
	}

	for _, tt := range tests {
		t.Run(tt.status, func(t *testing.T) {
			if got := IsKeyStatusUsable(tt.status); got != tt.want {
				t.Errorf("IsKeyStatusUsable(%q) = %v, want %v", tt.status, got, tt.want)
			}
		})
	}
}

func TestResolveCallerID(t *testing.T) {
	tests := []struct {
		name string
		ctx  map[string]interface{}
		role Role
		want string
	}{
		{
			name: "BAP role returns bpp_id",
			ctx:  map[string]interface{}{"bpp_id": "bpp.example.com"},
			role: RoleBAP,
			want: "bpp.example.com",
		},
		{
			name: "BAP role falls back to bppId",
			ctx:  map[string]interface{}{"bppId": "bpp.example.com"},
			role: RoleBAP,
			want: "bpp.example.com",
		},
		{
			name: "BAP role falls back to receiverId",
			ctx:  map[string]interface{}{"receiverId": "bpp.example.com"},
			role: RoleBAP,
			want: "bpp.example.com",
		},
		{
			name: "BAP role: bpp_id takes precedence over bppId and receiverId",
			ctx:  map[string]interface{}{"bpp_id": "primary.com", "bppId": "camel.com", "receiverId": "v2.com"},
			role: RoleBAP,
			want: "primary.com",
		},
		{
			name: "BPP role returns bap_id",
			ctx:  map[string]interface{}{"bap_id": "bap.example.com"},
			role: RoleBPP,
			want: "bap.example.com",
		},
		{
			name: "BPP role falls back to bapId",
			ctx:  map[string]interface{}{"bapId": "bap.example.com"},
			role: RoleBPP,
			want: "bap.example.com",
		},
		{
			name: "BPP role falls back to senderId",
			ctx:  map[string]interface{}{"senderId": "bap.example.com"},
			role: RoleBPP,
			want: "bap.example.com",
		},
		{
			name: "BPP role: bap_id takes precedence over bapId and senderId",
			ctx:  map[string]interface{}{"bap_id": "primary.com", "bapId": "camel.com", "senderId": "v2.com"},
			role: RoleBPP,
			want: "primary.com",
		},
		{
			name: "Gateway role returns empty",
			ctx:  map[string]interface{}{"bap_id": "bap.example.com", "bpp_id": "bpp.example.com"},
			role: RoleGateway,
			want: "",
		},
		{
			name: "Empty context map returns empty",
			ctx:  map[string]interface{}{},
			role: RoleBPP,
			want: "",
		},
		{
			name: "No matching key returns empty",
			ctx:  map[string]interface{}{"some_other_field": "value"},
			role: RoleBPP,
			want: "",
		},
		{
			name: "Non-string value is skipped",
			ctx:  map[string]interface{}{"bap_id": 12345},
			role: RoleBPP,
			want: "",
		},
		{
			name: "Empty string value is skipped",
			ctx:  map[string]interface{}{"bap_id": "", "bapId": "bap.example.com"},
			role: RoleBPP,
			want: "bap.example.com",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := ResolveCallerID(tc.ctx, tc.role)
			if got != tc.want {
				t.Errorf("ResolveCallerID() = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestResolveSubscriberID(t *testing.T) {
	tests := []struct {
		name string
		ctx  map[string]interface{}
		role Role
		want string
	}{
		{
			name: "BAP role returns bap_id",
			ctx:  map[string]interface{}{"bap_id": "bap.example.com"},
			role: RoleBAP,
			want: "bap.example.com",
		},
		{
			name: "BAP role falls back to bapId",
			ctx:  map[string]interface{}{"bapId": "bap.example.com"},
			role: RoleBAP,
			want: "bap.example.com",
		},
		{
			name: "BAP role falls back to senderId",
			ctx:  map[string]interface{}{"senderId": "bap.example.com"},
			role: RoleBAP,
			want: "bap.example.com",
		},
		{
			name: "BAP role: bap_id takes precedence",
			ctx:  map[string]interface{}{"bap_id": "primary.com", "bapId": "camel.com", "senderId": "v2.com"},
			role: RoleBAP,
			want: "primary.com",
		},
		{
			name: "BPP role returns bpp_id",
			ctx:  map[string]interface{}{"bpp_id": "bpp.example.com"},
			role: RoleBPP,
			want: "bpp.example.com",
		},
		{
			name: "BPP role falls back to bppId",
			ctx:  map[string]interface{}{"bppId": "bpp.example.com"},
			role: RoleBPP,
			want: "bpp.example.com",
		},
		{
			name: "BPP role falls back to receiverId",
			ctx:  map[string]interface{}{"receiverId": "bpp.example.com"},
			role: RoleBPP,
			want: "bpp.example.com",
		},
		{
			name: "Gateway role returns empty",
			ctx:  map[string]interface{}{"bap_id": "bap.example.com", "bpp_id": "bpp.example.com"},
			role: RoleGateway,
			want: "",
		},
		{
			name: "Empty context map returns empty",
			ctx:  map[string]interface{}{},
			role: RoleBAP,
			want: "",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := ResolveSubscriberID(tc.ctx, tc.role)
			if got != tc.want {
				t.Errorf("ResolveSubscriberID() = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestResolveNetworkID(t *testing.T) {
	tests := []struct {
		name string
		ctx  map[string]interface{}
		want string
	}{
		{name: "snake_case key", ctx: map[string]interface{}{"network_id": "nfo1.com/retail"}, want: "nfo1.com/retail"},
		{name: "camelCase key", ctx: map[string]interface{}{"networkId": "nfo1.com/retail"}, want: "nfo1.com/retail"},
		{name: "snake_case takes precedence over camelCase", ctx: map[string]interface{}{"network_id": "nfo1.com/retail", "networkId": "nfo2.com/retail"}, want: "nfo1.com/retail"},
		{name: "non-string snake_case falls through to camelCase", ctx: map[string]interface{}{"network_id": 12345, "networkId": "nfo1.com/retail"}, want: "nfo1.com/retail"},
		{name: "absent returns empty", ctx: map[string]interface{}{}, want: ""},
		{name: "empty string returns empty", ctx: map[string]interface{}{"network_id": ""}, want: ""},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := ResolveNetworkID(tc.ctx)
			if got != tc.want {
				t.Errorf("ResolveNetworkID() = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestExtractContext(t *testing.T) {
	t.Run("malformed json", func(t *testing.T) {
		_, _, becknErr := ExtractContext([]byte("{"))
		if becknErr == nil {
			t.Fatal("expected an error for malformed JSON, got nil")
		}
		if becknErr.Code != "SCH_INVALID_JSON" {
			t.Errorf("Code = %s, want SCH_INVALID_JSON", becknErr.Code)
		}
		var syntaxErr *json.SyntaxError
		if !errors.As(becknErr, &syntaxErr) {
			t.Error("expected errors.As on becknErr to reach the underlying *json.SyntaxError via Unwrap")
		}
	})

	t.Run("missing context", func(t *testing.T) {
		_, _, becknErr := ExtractContext([]byte(`{"message":{}}`))
		if becknErr == nil {
			t.Fatal("expected an error for missing context, got nil")
		}
		if becknErr.Code != "SCH_REQUIRED_FIELD_MISSING" {
			t.Errorf("Code = %s, want SCH_REQUIRED_FIELD_MISSING", becknErr.Code)
		}
		if becknErr.Message != "context field not found or invalid" {
			t.Errorf("Message = %q, want %q", becknErr.Message, "context field not found or invalid")
		}
		if becknErr.Unwrap() != nil {
			t.Errorf("expected a nil Unwrap() for a missing-context failure (no wrapped error of its own), got %v", becknErr.Unwrap())
		}
	})

	t.Run("context is not an object", func(t *testing.T) {
		_, _, becknErr := ExtractContext([]byte(`{"context":"not-a-map"}`))
		if becknErr == nil {
			t.Fatal("expected an error for non-object context, got nil")
		}
		if becknErr.Code != "SCH_REQUIRED_FIELD_MISSING" {
			t.Errorf("Code = %s, want SCH_REQUIRED_FIELD_MISSING", becknErr.Code)
		}
	})

	t.Run("valid body returns both the full body and its context", func(t *testing.T) {
		req, reqContext, becknErr := ExtractContext([]byte(`{"context":{"bap_id":"bap-123"},"message":{"key":"value"}}`))
		if becknErr != nil {
			t.Fatalf("unexpected error: %+v", becknErr)
		}
		if req["message"] == nil {
			t.Errorf("expected req to include the full decoded body, got %+v", req)
		}
		if reqContext["bap_id"] != "bap-123" {
			t.Errorf("reqContext[\"bap_id\"] = %v, want bap-123", reqContext["bap_id"])
		}
	})
}

func TestWrapExtractContextErr(t *testing.T) {
	t.Run("with cause, wraps prefix and preserves errors.As reachability", func(t *testing.T) {
		_, _, becknErr := ExtractContext([]byte("{"))
		err := WrapExtractContextErr("error parsing request body", becknErr)

		if err.Code != "SCH_INVALID_JSON" {
			t.Errorf("Code = %s, want SCH_INVALID_JSON", err.Code)
		}
		if !strings.Contains(err.Error(), "error parsing request body") {
			t.Errorf("Error() = %q, want it to contain the given prefix", err.Error())
		}
		var syntaxErr *json.SyntaxError
		if !errors.As(err, &syntaxErr) {
			t.Error("expected errors.As to reach the underlying *json.SyntaxError")
		}
	})

	t.Run("without cause, uses becknErr.Message directly", func(t *testing.T) {
		_, _, becknErr := ExtractContext([]byte(`{"message":{}}`))
		err := WrapExtractContextErr("unused prefix", becknErr)

		if err.Code != "SCH_REQUIRED_FIELD_MISSING" {
			t.Errorf("Code = %s, want SCH_REQUIRED_FIELD_MISSING", err.Code)
		}
		if !strings.Contains(err.Error(), "context field not found or invalid") {
			t.Errorf("Error() = %q, want it to contain becknErr.Message", err.Error())
		}
	})
}
