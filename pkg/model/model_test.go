package model

import "testing"

func TestResolveCallerID(t *testing.T) {
	tests := []struct {
		name    string
		ctx     map[string]interface{}
		role    Role
		want    string
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
