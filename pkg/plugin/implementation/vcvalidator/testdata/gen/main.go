//go:build ignore

// Command gen produces the mock Verifiable Credential test vectors used by the
// vcvalidator unit tests. Run it from the plugin directory:
//
//	go run ./testdata/gen
//
// Every key is derived from a fixed seed and every JSON document is emitted
// with stable field ordering, so regenerating yields byte-identical files —
// re-run it only when you intend to change a vector, and commit the result.
//
// It emits, under testdata/vectors/:
//
//	didkey-unrevoked.json   VC issued by a did:key (Ed25519) issuer, not revoked
//	didkey-revoked.json     same issuer, credentialStatus bit set  -> revoked
//	didjwk-unrevoked.json   VC issued by a did:jwk (Ed25519) issuer, not revoked
//	didjwk-revoked.json     same issuer, credentialStatus bit set  -> revoked
//	didweb-unrevoked.json   VC issued by a did:web issuer, not revoked
//	didweb-revoked.json     same issuer, credentialStatus bit set  -> revoked
//	didweb-did.json         the DID document served at the did:web URL
//	statuslist.json         the StatusList2021 credential referenced above
package main

import (
	"bytes"
	"compress/gzip"
	"crypto/ed25519"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
)

// Shared constants. The test wires an in-memory fetcher that maps these URLs to
// the generated documents, so nothing here is fetched over the network.
const (
	didWebDID     = "did:web:issuer.example.org"
	didWebDocURL  = "https://issuer.example.org/.well-known/did.json"
	statusListURL = "https://status.example.org/revocation/1"

	revokedIndex   = 94 // bit set in statuslist.json
	unrevokedIndex = 17 // bit clear in statuslist.json

	validFrom  = "2026-01-01T00:00:00Z"
	validUntil = "2027-12-31T23:59:59Z"
	nbf        = 1767225600 // 2026-01-01T00:00:00Z
	exp        = 1830297599 // 2027-12-31T23:59:59Z
	iat        = 1767225600
)

func main() {
	outDir := filepath.Join("testdata", "vectors")
	if err := os.MkdirAll(outDir, 0o755); err != nil {
		log.Fatal(err)
	}

	// Deterministic issuer keys (32-byte Ed25519 seeds).
	keyIssuer := ed25519.NewKeyFromSeed(seed("vcvalidator-did-key-issuer-seed!"))
	jwkIssuer := ed25519.NewKeyFromSeed(seed("vcvalidator-did-jwk-issuer-seed!"))
	webIssuer := ed25519.NewKeyFromSeed(seed("vcvalidator-did-web-issuer-seed!"))

	// ── did:key issuer ──────────────────────────────────────────────────
	keyDID := didKey(keyIssuer.Public().(ed25519.PublicKey))
	write(outDir, "didkey-unrevoked.json", buildVC(vcParams{
		id:         "urn:uuid:11111111-1111-1111-1111-111111111111",
		issuer:     rawString(keyDID),
		subject:    `{"id":"did:example:consumer-001","role":"data-consumer"}`,
		statusIdx:  unrevokedIndex,
		signKey:    keyIssuer,
		kid:        keyDID,
		issuerBind: keyDID,
	}))
	write(outDir, "didkey-revoked.json", buildVC(vcParams{
		id:         "urn:uuid:11111111-1111-1111-1111-111111111112",
		issuer:     rawString(keyDID),
		subject:    `{"id":"did:example:consumer-001","role":"data-consumer"}`,
		statusIdx:  revokedIndex,
		signKey:    keyIssuer,
		kid:        keyDID,
		issuerBind: keyDID,
	}))

	// ── did:jwk issuer ──────────────────────────────────────────────────
	jwkDID := didJWK(jwkIssuer.Public().(ed25519.PublicKey))
	write(outDir, "didjwk-unrevoked.json", buildVC(vcParams{
		id:         "urn:uuid:22222222-2222-2222-2222-222222222221",
		issuer:     rawString(jwkDID),
		subject:    `{"id":"did:example:consumer-002","role":"data-consumer"}`,
		statusIdx:  unrevokedIndex,
		signKey:    jwkIssuer,
		kid:        jwkDID + "#0",
		issuerBind: jwkDID,
	}))
	write(outDir, "didjwk-revoked.json", buildVC(vcParams{
		id:         "urn:uuid:22222222-2222-2222-2222-222222222222",
		issuer:     rawString(jwkDID),
		subject:    `{"id":"did:example:consumer-002","role":"data-consumer"}`,
		statusIdx:  revokedIndex,
		signKey:    jwkIssuer,
		kid:        jwkDID + "#0",
		issuerBind: jwkDID,
	}))

	// ── did:web issuer ──────────────────────────────────────────────────
	webKID := didWebDID + "#key-1"
	webPub := webIssuer.Public().(ed25519.PublicKey)
	// issuer as an object {id,name} to exercise the object form too.
	webIssuerObj := fmt.Sprintf(`{"id":%q,"name":"Example Grid Operator"}`, didWebDID)
	write(outDir, "didweb-did.json", buildDIDWebDoc(webKID, webPub))
	write(outDir, "didweb-unrevoked.json", buildVC(vcParams{
		id:         "urn:uuid:33333333-3333-3333-3333-333333333331",
		issuer:     json.RawMessage(webIssuerObj),
		subject:    `{"id":"did:example:consumer-003","role":"data-consumer"}`,
		statusIdx:  unrevokedIndex,
		signKey:    webIssuer,
		kid:        webKID,
		issuerBind: didWebDID,
	}))
	write(outDir, "didweb-revoked.json", buildVC(vcParams{
		id:         "urn:uuid:33333333-3333-3333-3333-333333333332",
		issuer:     json.RawMessage(webIssuerObj),
		subject:    `{"id":"did:example:consumer-003","role":"data-consumer"}`,
		statusIdx:  revokedIndex,
		signKey:    webIssuer,
		kid:        webKID,
		issuerBind: didWebDID,
	}))

	// ── shared StatusList2021 credential ────────────────────────────────
	write(outDir, "statuslist.json", buildStatusList(revokedIndex))

	fmt.Println("wrote vectors to", outDir)
}

type vcParams struct {
	id         string
	issuer     json.RawMessage
	subject    string // raw JSON object
	statusIdx  int
	signKey    ed25519.PrivateKey
	kid        string
	issuerBind string // DID the JWT must be bound to (== issuer DID)
}

// vcDoc mirrors the W3C VC fields the validator inspects. Field order is fixed
// so the marshalled output is stable.
type vcDoc struct {
	Context           []string        `json:"@context"`
	ID                string          `json:"id"`
	Type              []string        `json:"type"`
	Issuer            json.RawMessage `json:"issuer"`
	ValidFrom         string          `json:"validFrom"`
	ValidUntil        string          `json:"validUntil"`
	CredentialStatus  json.RawMessage `json:"credentialStatus"`
	CredentialSubject json.RawMessage `json:"credentialSubject"`
	Proof             proofDoc        `json:"proof"`
}

type proofDoc struct {
	Type string `json:"type"`
	JWT  string `json:"jwt"`
}

func buildVC(p vcParams) []byte {
	status := fmt.Sprintf(
		`{"id":%q,"type":"StatusList2021Entry","statusPurpose":"revocation","statusListIndex":"%d","statusListCredential":%q}`,
		fmt.Sprintf("%s#%d", statusListURL, p.statusIdx), p.statusIdx, statusListURL)

	jwt := signJWT(p.signKey, p.kid, jwtClaims{
		Iss: p.issuerBind,
		Sub: "did:example:subject",
		Nbf: nbf,
		Exp: exp,
		Iat: iat,
	})

	doc := vcDoc{
		Context: []string{
			"https://www.w3.org/ns/credentials/v2",
			"https://w3id.org/vc/status-list/2021/v1",
		},
		ID:                p.id,
		Type:              []string{"VerifiableCredential", "MeterDataRequestCredential"},
		Issuer:            p.issuer,
		ValidFrom:         validFrom,
		ValidUntil:        validUntil,
		CredentialStatus:  json.RawMessage(status),
		CredentialSubject: json.RawMessage(p.subject),
		Proof:             proofDoc{Type: "JsonWebSignature2020", JWT: jwt},
	}
	return marshal(doc)
}

// buildDIDWebDoc emits the DID document served at the did:web URL.
func buildDIDWebDoc(kid string, pub ed25519.PublicKey) []byte {
	type vm struct {
		ID           string          `json:"id"`
		Type         string          `json:"type"`
		Controller   string          `json:"controller"`
		PublicKeyJwk json.RawMessage `json:"publicKeyJwk"`
	}
	type doc struct {
		Context            []string `json:"@context"`
		ID                 string   `json:"id"`
		VerificationMethod []vm     `json:"verificationMethod"`
		AssertionMethod    []string `json:"assertionMethod"`
	}
	return marshal(doc{
		Context: []string{
			"https://www.w3.org/ns/did/v1",
			"https://w3id.org/security/suites/jws-2020/v1",
		},
		ID: didWebDID,
		VerificationMethod: []vm{{
			ID:           kid,
			Type:         "JsonWebKey2020",
			Controller:   didWebDID,
			PublicKeyJwk: json.RawMessage(ed25519JWK(pub)),
		}},
		AssertionMethod: []string{kid},
	})
}

// buildStatusList emits a StatusList2021 credential whose bitstring has exactly
// the given index set (revoked); all other bits — including unrevokedIndex —
// are clear.
func buildStatusList(setIdx int) []byte {
	bits := make([]byte, 16384) // 131072 entries, the StatusList2021 minimum
	bits[setIdx/8] |= 0x80 >> uint(setIdx%8)

	var buf bytes.Buffer
	zw := gzip.NewWriter(&buf)
	if _, err := zw.Write(bits); err != nil {
		log.Fatal(err)
	}
	if err := zw.Close(); err != nil {
		log.Fatal(err)
	}
	encoded := base64.RawURLEncoding.EncodeToString(buf.Bytes())

	type subject struct {
		ID            string `json:"id"`
		Type          string `json:"type"`
		StatusPurpose string `json:"statusPurpose"`
		EncodedList   string `json:"encodedList"`
	}
	type doc struct {
		Context           []string `json:"@context"`
		ID                string   `json:"id"`
		Type              []string `json:"type"`
		Issuer            string   `json:"issuer"`
		ValidFrom         string   `json:"validFrom"`
		CredentialSubject subject  `json:"credentialSubject"`
	}
	return marshal(doc{
		Context:   []string{"https://www.w3.org/ns/credentials/v2", "https://w3id.org/vc/status-list/2021/v1"},
		ID:        statusListURL,
		Type:      []string{"VerifiableCredential", "StatusList2021Credential"},
		Issuer:    didWebDID,
		ValidFrom: validFrom,
		CredentialSubject: subject{
			ID:            statusListURL + "#list",
			Type:          "StatusList2021",
			StatusPurpose: "revocation",
			EncodedList:   encoded,
		},
	})
}

// ── DID + JWT helpers ───────────────────────────────────────────────────

type jwtClaims struct {
	Iss string `json:"iss"`
	Sub string `json:"sub"`
	Nbf int64  `json:"nbf"`
	Exp int64  `json:"exp"`
	Iat int64  `json:"iat"`
}

// signJWT builds a compact EdDSA JWS by hand so the output is fully
// deterministic (no library header-ordering surprises).
func signJWT(key ed25519.PrivateKey, kid string, claims jwtClaims) string {
	header := fmt.Sprintf(`{"alg":"EdDSA","kid":%q,"typ":"JWT"}`, kid)
	payload := marshal(claims)
	signingInput := b64(header) + "." + b64(string(payload))
	sig := ed25519.Sign(key, []byte(signingInput))
	return signingInput + "." + base64.RawURLEncoding.EncodeToString(sig)
}

// didKey encodes an Ed25519 public key as a did:key (multicodec 0xed01).
func didKey(pub ed25519.PublicKey) string {
	mc := append([]byte{0xed, 0x01}, pub...)
	return "did:key:z" + base58Encode(mc)
}

// didJWK encodes an Ed25519 public key as a did:jwk.
func didJWK(pub ed25519.PublicKey) string {
	return "did:jwk:" + base64.RawURLEncoding.EncodeToString([]byte(ed25519JWK(pub)))
}

// ed25519JWK returns the canonical public JWK JSON for an Ed25519 key.
func ed25519JWK(pub ed25519.PublicKey) string {
	return fmt.Sprintf(`{"crv":"Ed25519","kty":"OKP","x":%q}`, base64.RawURLEncoding.EncodeToString(pub))
}

func base58Encode(b []byte) string {
	const alpha = "123456789ABCDEFGHJKLMNPQRSTUVWXYZabcdefghijkmnopqrstuvwxyz"
	zeros := 0
	for zeros < len(b) && b[zeros] == 0 {
		zeros++
	}
	digits := []int{0}
	for i := zeros; i < len(b); i++ {
		carry := int(b[i])
		for j := 0; j < len(digits); j++ {
			carry += digits[j] << 8
			digits[j] = carry % 58
			carry /= 58
		}
		for carry > 0 {
			digits = append(digits, carry%58)
			carry /= 58
		}
	}
	var sb strings.Builder
	for i := 0; i < zeros; i++ {
		sb.WriteByte('1')
	}
	for i := len(digits) - 1; i >= 0; i-- {
		sb.WriteByte(alpha[digits[i]])
	}
	return sb.String()
}

// ── small utilities ─────────────────────────────────────────────────────

func seed(s string) []byte {
	if len(s) != ed25519.SeedSize {
		log.Fatalf("seed %q must be %d bytes, got %d", s, ed25519.SeedSize, len(s))
	}
	return []byte(s)
}

func rawString(s string) json.RawMessage {
	b, _ := json.Marshal(s)
	return b
}

func b64(s string) string { return base64.RawURLEncoding.EncodeToString([]byte(s)) }

func marshal(v any) []byte {
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		log.Fatal(err)
	}
	return append(b, '\n')
}

func write(dir, name string, data []byte) {
	if err := os.WriteFile(filepath.Join(dir, name), data, 0o644); err != nil {
		log.Fatal(err)
	}
}
