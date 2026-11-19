package keymanager

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	vault "github.com/hashicorp/vault/api"
)

// Keyset runs on every signing request. A secret that lacks a field, or
// carries one with the wrong type, must be reported as an error; a panic
// would take the request down with no usable error for the operator.
func TestKeyset_MalformedSecretIsAnErrorNotAPanic(t *testing.T) {
	cases := map[string]string{
		"field missing":      `{"data":{"uniqueKeyID":"k1","signingPublicKey":"sp","encrPublicKey":"ep","encrPrivateKey":"epr"}}`,
		"field not a string": `{"data":{"uniqueKeyID":"k1","signingPublicKey":"sp","signingPrivateKey":42,"encrPublicKey":"ep","encrPrivateKey":"epr"}}`,
		"field null":         `{"data":{"uniqueKeyID":null,"signingPublicKey":"sp","signingPrivateKey":"s","encrPublicKey":"ep","encrPrivateKey":"epr"}}`,
	}
	for name, secret := range cases {
		t.Run(name, func(t *testing.T) {
			ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				_, _ = w.Write([]byte(secret))
			}))
			defer ts.Close()

			cfg := vault.DefaultConfig()
			cfg.Address = ts.URL
			client, err := vault.NewClient(cfg)
			if err != nil {
				t.Fatal(err)
			}
			km := &KeyMgr{VaultClient: client, KvVersion: "v1"}

			defer func() {
				if r := recover(); r != nil {
					t.Fatalf("Keyset panicked on a malformed secret: %v", r)
				}
			}()
			ks, err := km.Keyset(context.Background(), "k1")
			if err == nil {
				t.Fatalf("Keyset returned %+v and no error for a malformed secret", ks)
			}
			if strings.Contains(err.Error(), "epr") || strings.Contains(err.Error(), "42") {
				t.Errorf("error must not echo secret values: %v", err)
			}
		})
	}
}

// A Vault read that fails or finds nothing must give an error that says so
// (previously "%!w(<nil>)" when the secret was simply absent).
func TestKeyset_AbsentSecretHasReadableError(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	defer ts.Close()
	cfg := vault.DefaultConfig()
	cfg.Address = ts.URL
	client, _ := vault.NewClient(cfg)
	km := &KeyMgr{VaultClient: client, KvVersion: "v1"}

	_, err := km.Keyset(context.Background(), "nope")
	if err == nil {
		t.Fatal("expected an error for an absent secret")
	}
	if strings.Contains(err.Error(), "%!w") {
		t.Errorf("error is malformed: %q", err.Error())
	}
}
