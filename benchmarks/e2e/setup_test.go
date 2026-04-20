package e2e_bench_test

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/beckn-one/beckn-onix/core/module"
	"github.com/beckn-one/beckn-onix/core/module/handler"
	"github.com/beckn-one/beckn-onix/pkg/model"
	"github.com/beckn-one/beckn-onix/pkg/plugin"
	"github.com/google/uuid"
	"github.com/rs/zerolog"
	"golang.org/x/crypto/blake2b"
)

// Package-level references shared across all benchmarks.
var (
	adapterServer *httptest.Server
	miniRedis     *miniredis.Miniredis
	mockBPP       *httptest.Server
	mockRegistry  *httptest.Server
	pluginDir     string
	moduleRoot    string // set in TestMain; used by buildBAPCallerConfig for local file paths
)

// Plugins to compile for the benchmark. Each entry is (pluginID, source path relative to module root).
var pluginsToBuild = []struct {
	id  string
	src string
}{
	{"router", "pkg/plugin/implementation/router/cmd/plugin.go"},
	{"signer", "pkg/plugin/implementation/signer/cmd/plugin.go"},
	{"signvalidator", "pkg/plugin/implementation/signvalidator/cmd/plugin.go"},
	{"simplekeymanager", "pkg/plugin/implementation/simplekeymanager/cmd/plugin.go"},
	{"cache", "pkg/plugin/implementation/cache/cmd/plugin.go"},
	{"schemav2validator", "pkg/plugin/implementation/schemav2validator/cmd/plugin.go"},
	{"otelsetup", "pkg/plugin/implementation/otelsetup/cmd/plugin.go"},
	// registry is required by stdHandler to wire KeyManager, even on the caller
	// path where sign-validation never runs.
	{"registry", "pkg/plugin/implementation/registry/cmd/plugin.go"},
}

// TestMain is the entry point for the benchmark package. It:
//  1. Compiles all required .so plugins into a temp directory
//  2. Starts miniredis (in-process Redis)
//  3. Starts mock BPP and registry HTTP servers
//  4. Starts the adapter as an httptest.Server
//  5. Runs all benchmarks
//  6. Tears everything down in reverse order
func TestMain(m *testing.M) {
	ctx := context.Background()

	// ── Step 1: Compile plugins ───────────────────────────────────────────────
	var err error
	pluginDir, err = os.MkdirTemp("", "beckn-bench-plugins-*")
	if err != nil {
		fmt.Fprintf(os.Stderr, "ERROR: failed to create plugin temp dir: %v\n", err)
		os.Exit(1)
	}
	defer os.RemoveAll(pluginDir)

	moduleRoot, err = findModuleRoot()
	if err != nil {
		fmt.Fprintf(os.Stderr, "ERROR: failed to locate module root: %v\n", err)
		os.Exit(1)
	}

	fmt.Printf("=== Building plugins (first run may take 60-90s) ===\n")
	for _, p := range pluginsToBuild {
		outPath := filepath.Join(pluginDir, p.id+".so")
		srcPath := filepath.Join(moduleRoot, p.src)
		fmt.Printf("  compiling %s.so ...\n", p.id)
		cmd := exec.Command("go", "build", "-buildmode=plugin", "-o", outPath, srcPath)
		cmd.Dir = moduleRoot
		if out, buildErr := cmd.CombinedOutput(); buildErr != nil {
			fmt.Fprintf(os.Stderr, "ERROR: failed to build plugin %s:\n%s\n", p.id, string(out))
			os.Exit(1)
		}
	}
	fmt.Printf("=== All plugins compiled successfully ===\n\n")

	// ── Step 2: Start miniredis ───────────────────────────────────────────────
	miniRedis, err = miniredis.Run()
	if err != nil {
		fmt.Fprintf(os.Stderr, "ERROR: failed to start miniredis: %v\n", err)
		os.Exit(1)
	}
	defer miniRedis.Close()

	// ── Step 3: Start mock servers ────────────────────────────────────────────
	mockBPP = startMockBPP()
	defer mockBPP.Close()

	mockRegistry = startMockRegistry()
	defer mockRegistry.Close()

	// ── Step 4: Start adapter ─────────────────────────────────────────────────
	adapterServer, err = startAdapter(ctx)
	if err != nil {
		fmt.Fprintf(os.Stderr, "ERROR: failed to start adapter: %v\n", err)
		os.Exit(1)
	}
	defer adapterServer.Close()

	// ── Step 5: Run benchmarks ────────────────────────────────────────────────
	// Silence the adapter's zerolog output for the duration of the benchmark
	// run. Without this, every HTTP request the adapter processes emits a JSON
	// log line to stdout, which interleaves with Go's benchmark result lines
	// (BenchmarkFoo-N\t\t<count>\t<ns/op>) and makes benchstat unparseable.
	// Setup logging above still ran normally; zerolog.Disabled is set only here,
	// just before m.Run(), so errors during startup remain visible.
	zerolog.SetGlobalLevel(zerolog.Disabled)
	os.Exit(m.Run())
}

// findModuleRoot walks up from the current directory to find the go.mod root.
func findModuleRoot() (string, error) {
	dir, err := os.Getwd()
	if err != nil {
		return "", err
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir, nil
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", fmt.Errorf("go.mod not found from %s", dir)
		}
		dir = parent
	}
}

// writeRoutingConfig reads the benchmark routing config template, replaces the
// BENCH_BPP_URL placeholder with the live mock BPP server URL, and writes the
// result to a temp file. Returns the path to the temp file.
func writeRoutingConfig(bppURL string) (string, error) {
	templatePath := filepath.Join("testdata", "routing-BAPCaller.yaml")
	data, err := os.ReadFile(templatePath)
	if err != nil {
		return "", fmt.Errorf("reading routing config template: %w", err)
	}
	content := strings.ReplaceAll(string(data), "BENCH_BPP_URL", bppURL)
	f, err := os.CreateTemp("", "bench-routing-*.yaml")
	if err != nil {
		return "", fmt.Errorf("creating temp routing config: %w", err)
	}
	if _, err := f.WriteString(content); err != nil {
		f.Close()
		return "", fmt.Errorf("writing routing config: %w", err)
	}
	f.Close()
	return f.Name(), nil
}

// startAdapter constructs a fully wired adapter using the compiled plugins and
// returns it as an *httptest.Server. All external dependencies are replaced with
// local mock servers: Redis → miniredis, BPP → mockBPP, registry → mockRegistry.
func startAdapter(ctx context.Context) (*httptest.Server, error) {
	routingConfigPath, err := writeRoutingConfig(mockBPP.URL)
	if err != nil {
		return nil, fmt.Errorf("writing routing config: %w", err)
	}

	// Plugin manager: load all compiled .so files from pluginDir.
	mgr, closer, err := plugin.NewManager(ctx, &plugin.ManagerConfig{
		Root: pluginDir,
	})
	if err != nil {
		return nil, fmt.Errorf("creating plugin manager: %w", err)
	}
	_ = closer // closer is called when the server shuts down; deferred in TestMain via server.Close

	// Build module configurations.
	mCfgs := []module.Config{
		buildBAPCallerConfig(routingConfigPath, mockRegistry.URL),
	}

	mux := http.NewServeMux()
	if err := module.Register(ctx, mCfgs, mux, mgr); err != nil {
		return nil, fmt.Errorf("registering modules: %w", err)
	}

	srv := httptest.NewServer(mux)
	return srv, nil
}

// buildBAPCallerConfig returns the module.Config for the bapTxnCaller handler,
// mirroring config/local-retail-bap.yaml but pointing at benchmark mock services.
// registryURL must point at the mock registry so simplekeymanager can satisfy the
// Registry requirement imposed by stdHandler — even though the caller path never
// performs signature validation, the handler wiring requires it to be present.
func buildBAPCallerConfig(routingConfigPath, registryURL string) module.Config {
	return module.Config{
		Name: "bapTxnCaller",
		Path: "/bap/caller/",
		Handler: handler.Config{
			Type:         handler.HandlerTypeStd,
			Role:         model.RoleBAP,
			SubscriberID: benchSubscriberID,
			HttpClientConfig: handler.HttpClientConfig{
				MaxIdleConns:          1000,
				MaxIdleConnsPerHost:   200,
				IdleConnTimeout:       300 * time.Second,
				ResponseHeaderTimeout: 5 * time.Second,
			},
			Plugins: handler.PluginCfg{
				// Registry is required by stdHandler before it will wire KeyManager,
				// even on the caller path where sign-validation never runs. We point
				// it at the mock registry (retry_max=0 so failures are immediate).
				Registry: &plugin.Config{
					ID: "registry",
					Config: map[string]string{
						"url":       registryURL,
						"retry_max": "0",
					},
				},
				KeyManager: &plugin.Config{
					ID: "simplekeymanager",
					Config: map[string]string{
						"networkParticipant": benchSubscriberID,
						"keyId":              benchKeyID,
						"signingPrivateKey":  benchPrivKey,
						"signingPublicKey":   benchPubKey,
						"encrPrivateKey":     benchEncrPrivKey,
						"encrPublicKey":      benchEncrPubKey,
					},
				},
				SchemaValidator: &plugin.Config{
					ID: "schemav2validator",
					Config: map[string]string{
						"type":     "file",
						"location": filepath.Join(moduleRoot, "benchmarks/e2e/testdata/beckn.yaml"),
						"cacheTTL": "3600",
					},
				},
				Cache: &plugin.Config{
					ID: "cache",
					Config: map[string]string{
						"addr": miniRedis.Addr(),
					},
				},
				Router: &plugin.Config{
					ID: "router",
					Config: map[string]string{
						"routingConfig": routingConfigPath,
					},
				},
				Signer: &plugin.Config{
					ID: "signer",
				},
			},
			Steps: []string{"addRoute", "sign", "validateSchema"},
		},
	}
}

// ── Request builder and Beckn signing helper ─────────────────────────────────

// becknPayloadTemplate holds the raw JSON for a fixture file with sentinels.
var fixtureCache = map[string][]byte{}

// loadFixture reads a fixture file from testdata/ and caches it.
func loadFixture(action string) ([]byte, error) {
	if data, ok := fixtureCache[action]; ok {
		return data, nil
	}
	path := filepath.Join("testdata", action+"_request.json")
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("loading fixture %s: %w", action, err)
	}
	fixtureCache[action] = data
	return data, nil
}

// buildSignedRequest reads the fixture for the given action, substitutes
// BENCH_TIMESTAMP / BENCH_MESSAGE_ID / BENCH_TRANSACTION_ID with fresh values,
// signs the body using the Beckn Ed25519 spec, and returns a ready-to-send
// *http.Request targeting the adapter's /bap/caller/<action> path.
func buildSignedRequest(tb testing.TB, action string) *http.Request {
	tb.Helper()

	fixture, err := loadFixture(action)
	if err != nil {
		tb.Fatalf("buildSignedRequest: %v", err)
	}

	// Substitute sentinels with fresh values for this iteration.
	now := time.Now().UTC().Format(time.RFC3339)
	msgID := uuid.New().String()
	txnID := uuid.New().String()

	body := bytes.ReplaceAll(fixture, []byte("BENCH_TIMESTAMP"), []byte(now))
	body = bytes.ReplaceAll(body, []byte("BENCH_MESSAGE_ID"), []byte(msgID))
	body = bytes.ReplaceAll(body, []byte("BENCH_TRANSACTION_ID"), []byte(txnID))

	// Sign the body per the Beckn Ed25519 spec.
	authHeader, err := signBecknPayload(body)
	if err != nil {
		tb.Fatalf("buildSignedRequest: signing failed: %v", err)
	}

	url := adapterServer.URL + "/bap/caller/" + action
	req, err := http.NewRequest(http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		tb.Fatalf("buildSignedRequest: http.NewRequest: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set(model.AuthHeaderSubscriber, authHeader)

	return req
}

// buildSignedRequestFixed builds a signed request with a fixed body (same
// message_id every call) — used for cache-warm benchmarks.
func buildSignedRequestFixed(tb testing.TB, action string, body []byte) *http.Request {
	tb.Helper()

	authHeader, err := signBecknPayload(body)
	if err != nil {
		tb.Fatalf("buildSignedRequestFixed: signing failed: %v", err)
	}

	url := adapterServer.URL + "/bap/caller/" + action
	req, err := http.NewRequest(http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		tb.Fatalf("buildSignedRequestFixed: http.NewRequest: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set(model.AuthHeaderSubscriber, authHeader)
	return req
}

// signBecknPayload signs a request body using the Beckn Ed25519 signing spec
// and returns a formatted Authorization header value.
//
// Beckn signing spec:
//  1. Digest:  "BLAKE-512=" + base64(blake2b-512(body))
//  2. Signing string: "(created): <ts>\n(expires): <ts+5m>\ndigest: <digest>"
//  3. Signature: base64(ed25519.Sign(privKey, signingString))
//  4. Header: Signature keyId="<sub>|<keyId>|ed25519",algorithm="ed25519",
//     created="<ts>",expires="<ts+5m>",headers="(created) (expires) digest",
//     signature="<sig>"
//
// Reference: pkg/plugin/implementation/signer/signer.go
func signBecknPayload(body []byte) (string, error) {
	createdAt := time.Now().Unix()
	expiresAt := time.Now().Add(5 * time.Minute).Unix()

	// Step 1: BLAKE-512 digest.
	hasher, _ := blake2b.New512(nil)
	hasher.Write(body)
	digest := "BLAKE-512=" + base64.StdEncoding.EncodeToString(hasher.Sum(nil))

	// Step 2: Signing string.
	signingString := fmt.Sprintf("(created): %d\n(expires): %d\ndigest: %s", createdAt, expiresAt, digest)

	// Step 3: Ed25519 signature.
	privKeyBytes, err := base64.StdEncoding.DecodeString(benchPrivKey)
	if err != nil {
		return "", fmt.Errorf("decoding private key: %w", err)
	}
	privKey := ed25519.NewKeyFromSeed(privKeyBytes)
	sig := base64.StdEncoding.EncodeToString(ed25519.Sign(privKey, []byte(signingString)))

	// Step 4: Format Authorization header (matches generateAuthHeader in step.go).
	header := fmt.Sprintf(
		`Signature keyId="%s|%s|ed25519",algorithm="ed25519",created="%d",expires="%d",headers="(created) (expires) digest",signature="%s"`,
		benchSubscriberID, benchKeyID, createdAt, expiresAt, sig,
	)
	return header, nil
}

// warmFixtureBody returns a fixed body for the given action with stable IDs —
// used to pre-warm the cache so cache-warm benchmarks hit the Redis fast path.
func warmFixtureBody(tb testing.TB, action string) []byte {
	tb.Helper()
	fixture, err := loadFixture(action)
	if err != nil {
		tb.Fatalf("warmFixtureBody: %v", err)
	}
	body := bytes.ReplaceAll(fixture, []byte("BENCH_TIMESTAMP"), []byte("2025-01-01T00:00:00Z"))
	body = bytes.ReplaceAll(body, []byte("BENCH_MESSAGE_ID"), []byte("00000000-warm-0000-0000-000000000000"))
	body = bytes.ReplaceAll(body, []byte("BENCH_TRANSACTION_ID"), []byte("00000000-warm-txn-0000-000000000000"))
	return body
}

// sendRequest executes an HTTP request using the shared bench client and
// discards the response body. Returns a non-nil error for non-2xx responses.
func sendRequest(req *http.Request) error {
	resp, err := benchHTTPClient.Do(req)
	if err != nil {
		return fmt.Errorf("http do: %w", err)
	}
	defer resp.Body.Close()
	// Drain the body so the connection is returned to the pool for reuse.
	// Without this, Go discards the connection after each request, causing
	// port exhaustion under parallel load ("can't assign requested address").
	_, _ = io.Copy(io.Discard, resp.Body)
	// We accept any 2xx response (ACK or forwarded BPP response).
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("unexpected status: %d", resp.StatusCode)
	}
	return nil
}

// ── TestSignBecknPayload: validation test before running benchmarks ───────────
// Sends a signed discover request to the live adapter and asserts a 200 response,
// confirming the signing helper produces headers accepted by the adapter pipeline.
func TestSignBecknPayload(t *testing.T) {
	if adapterServer == nil {
		t.Skip("adapterServer not initialised (run via TestMain)")
	}
	fixture, err := loadFixture("discover")
	if err != nil {
		t.Fatalf("loading fixture: %v", err)
	}

	// Substitute sentinels.
	now := time.Now().UTC().Format(time.RFC3339)
	body := bytes.ReplaceAll(fixture, []byte("BENCH_TIMESTAMP"), []byte(now))
	body = bytes.ReplaceAll(body, []byte("BENCH_MESSAGE_ID"), []byte(uuid.New().String()))
	body = bytes.ReplaceAll(body, []byte("BENCH_TRANSACTION_ID"), []byte(uuid.New().String()))

	authHeader, err := signBecknPayload(body)
	if err != nil {
		t.Fatalf("signBecknPayload: %v", err)
	}

	url := adapterServer.URL + "/bap/caller/discover"
	req, err := http.NewRequest(http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		t.Fatalf("http.NewRequest: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set(model.AuthHeaderSubscriber, authHeader)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("sending request: %v", err)
	}
	defer resp.Body.Close()

	var result map[string]interface{}
	json.NewDecoder(resp.Body).Decode(&result)
	t.Logf("Response status: %d, body: %v", resp.StatusCode, result)

	if resp.StatusCode != http.StatusOK {
		t.Errorf("expected 200 OK, got %d", resp.StatusCode)
	}
}
