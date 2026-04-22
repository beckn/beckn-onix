package opapolicychecker

import (
	"bytes"
	"context"
	"crypto/ecdsa"
	"crypto/ed25519"
	"crypto/rsa"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"strings"
	"time"

	"github.com/open-policy-agent/opa/v1/ast"
	"github.com/open-policy-agent/opa/v1/bundle"
	"github.com/open-policy-agent/opa/v1/keys"
	"github.com/open-policy-agent/opa/v1/rego"
	"github.com/open-policy-agent/opa/v1/storage/inmem"

	"github.com/beckn-one/beckn-onix/pkg/security/artifactverifier"
)

// Evaluator wraps the OPA engine: loads and compiles .rego files at startup,
// then evaluates messages against the compiled policy set.
type Evaluator struct {
	preparedQuery   rego.PreparedEvalQuery
	query           string
	runtimeConfig   map[string]string
	moduleNames     []string // names of loaded .rego modules
	failOnUndefined bool     // if true, empty/undefined results are treated as violations
}

// ModuleNames returns the names of the loaded .rego policy modules.
func (e *Evaluator) ModuleNames() []string {
	return e.moduleNames
}

// defaultPolicyFetchTimeout bounds remote policy and bundle fetches during startup
// and refresh. This can be overridden via config.fetchTimeoutSeconds.
const defaultPolicyFetchTimeout = 30 * time.Second

// maxPolicySize is the maximum size of a single .rego file fetched from a URL (1 MB).
const maxPolicySize = 1 << 20

// maxBundleSize is the maximum size of a bundle archive (10 MB).
const maxBundleSize = 10 << 20

const defaultBundleVerificationKeyID = "default"
const defaultBundleVerificationAlgorithm = "ES256"

type ArtifactVerificationConfig struct {
	Enabled            bool
	PublicKeyLookupURL string
	SignatureLocation  string
	Algorithm          string
}

// NewEvaluator creates an Evaluator by loading .rego files from local paths
// and/or URLs, then compiling them. runtimeConfig is passed to Rego as data.config.
// When isBundle is true, the first policyPath is treated as a local path or URL to an OPA bundle (.tar.gz).
func NewEvaluator(policyPaths []string, query string, runtimeConfig map[string]string, isBundle bool, fetchTimeout time.Duration, verification *ArtifactVerificationConfig) (*Evaluator, error) {
	if fetchTimeout <= 0 {
		fetchTimeout = defaultPolicyFetchTimeout
	}
	if isBundle {
		return newBundleEvaluator(policyPaths, query, runtimeConfig, fetchTimeout, verification)
	}
	return newRegoEvaluator(policyPaths, query, runtimeConfig, fetchTimeout, verification)
}

// newRegoEvaluator loads raw .rego files from local paths and/or URLs.
func newRegoEvaluator(policyPaths []string, query string, runtimeConfig map[string]string, fetchTimeout time.Duration, verification *ArtifactVerificationConfig) (*Evaluator, error) {
	modules := make(map[string]string)

	if verification != nil && verification.Enabled {
		if len(policyPaths) != 1 {
			return nil, fmt.Errorf("artifact verification requires exactly one policy source")
		}

		name, policyBytes, err := loadSinglePolicy(policyPaths[0], fetchTimeout)
		if err != nil {
			return nil, fmt.Errorf("failed to load policy from %s: %w", policyPaths[0], err)
		}

		signatureBytes, err := readArtifact(verification.SignatureLocation, maxPolicySize, fetchTimeout)
		if err != nil {
			return nil, fmt.Errorf("failed to load detached signature from %s: %w", verification.SignatureLocation, err)
		}

		publicKeyBody, err := readArtifact(verification.PublicKeyLookupURL, maxPolicySize, fetchTimeout)
		if err != nil {
			return nil, fmt.Errorf("failed to load verification public key from %s: %w", verification.PublicKeyLookupURL, err)
		}

		if err := artifactverifier.VerifyDetachedArtifact(policyBytes, signatureBytes, publicKeyBody); err != nil {
			return nil, fmt.Errorf("policy signature verification failed: %w", err)
		}

		modules[name] = string(policyBytes)
		return compileAndPrepare(modules, nil, query, runtimeConfig, true)
	}

	// Load from policyPaths (resolved locations based on config Type)
	for _, source := range policyPaths {
		if isURL(source) {
			name, content, err := fetchPolicy(source, fetchTimeout)
			if err != nil {
				return nil, fmt.Errorf("failed to fetch policy from %s: %w", source, err)
			}
			modules[name] = content
		} else if info, err := os.Stat(source); err == nil && info.IsDir() {
			// Directory — load all .rego files inside
			entries, err := os.ReadDir(source)
			if err != nil {
				return nil, fmt.Errorf("failed to read policy directory %s: %w", source, err)
			}
			for _, entry := range entries {
				if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".rego") || strings.HasSuffix(entry.Name(), "_test.rego") {
					continue
				}
				fpath := filepath.Join(source, entry.Name())
				data, err := os.ReadFile(fpath)
				if err != nil {
					return nil, fmt.Errorf("failed to read policy file %s: %w", fpath, err)
				}
				modules[entry.Name()] = string(data)
			}
		} else {
			// Local file path
			data, err := os.ReadFile(source)
			if err != nil {
				return nil, fmt.Errorf("failed to read policy file %s: %w", source, err)
			}
			modules[filepath.Base(source)] = string(data)
		}
	}

	if len(modules) == 0 {
		return nil, fmt.Errorf("no .rego policy files found from any configured source")
	}

	return compileAndPrepare(modules, nil, query, runtimeConfig, true)
}

// newBundleEvaluator loads an OPA bundle (.tar.gz) from a local path or URL and compiles it.
func newBundleEvaluator(policyPaths []string, query string, runtimeConfig map[string]string, fetchTimeout time.Duration, verification *ArtifactVerificationConfig) (*Evaluator, error) {
	if len(policyPaths) == 0 {
		return nil, fmt.Errorf("bundle source is required")
	}

	bundleSource := policyPaths[0]
	modules, bundleData, err := loadBundle(bundleSource, fetchTimeout, verification)
	if err != nil {
		return nil, fmt.Errorf("failed to load bundle from %s: %w", bundleSource, err)
	}

	if len(modules) == 0 {
		return nil, fmt.Errorf("no .rego policy modules found in bundle from %s", bundleSource)
	}

	return compileAndPrepare(modules, bundleData, query, runtimeConfig, true)
}

// loadBundle downloads a .tar.gz OPA bundle from a URL, parses it using OPA's
// bundle reader, and returns the modules and data from the bundle.
func loadBundle(bundleSource string, fetchTimeout time.Duration, verification *ArtifactVerificationConfig) (map[string]string, map[string]interface{}, error) {
	data, err := readArtifact(bundleSource, maxBundleSize, fetchTimeout)
	if err != nil {
		return nil, nil, err
	}

	return parseBundleArchive(data, verification, fetchTimeout)
}

// parseBundleArchive parses a .tar.gz OPA bundle archive and extracts
// rego modules and data. Signature verification uses OPA's native bundle
// verification when enabled.
func parseBundleArchive(data []byte, verification *ArtifactVerificationConfig, fetchTimeout time.Duration) (map[string]string, map[string]interface{}, error) {
	loader := bundle.NewTarballLoaderWithBaseURL(bytes.NewReader(data), "")
	reader := bundle.NewCustomReader(loader).
		WithRegoVersion(ast.RegoV1)

	if verification != nil && verification.Enabled {
		publicKey, err := resolveVerificationPublicKey(verification.PublicKeyLookupURL, fetchTimeout)
		if err != nil {
			return nil, nil, fmt.Errorf("failed to load bundle verification public key from %s: %w", verification.PublicKeyLookupURL, err)
		}

		pemKey, err := publicKeyToPEM(publicKey)
		if err != nil {
			return nil, nil, fmt.Errorf("failed to encode bundle verification key: %w", err)
		}

		algorithm := verification.Algorithm
		if algorithm == "" {
			algorithm = defaultBundleVerificationAlgorithm
		}

		keyConfig, err := keys.NewKeyConfig(string(pemKey), algorithm, "")
		if err != nil {
			return nil, nil, fmt.Errorf("failed to parse bundle verification key: %w", err)
		}

		reader = reader.WithBundleVerificationConfig(
			bundle.NewVerificationConfig(map[string]*keys.Config{defaultBundleVerificationKeyID: keyConfig}, defaultBundleVerificationKeyID, "", nil),
		)
	} else {
		reader = reader.WithSkipBundleVerification(true)
	}

	b, err := reader.Read()
	if err != nil {
		return nil, nil, fmt.Errorf("failed to read bundle: %w", err)
	}

	modules := make(map[string]string, len(b.Modules))
	for _, m := range b.Modules {
		modules[m.Path] = string(m.Raw)
	}

	return modules, b.Data, nil
}

func loadSinglePolicy(source string, fetchTimeout time.Duration) (string, []byte, error) {
	if isURL(source) {
		name, content, err := fetchPolicy(source, fetchTimeout)
		if err != nil {
			return "", nil, err
		}
		return name, []byte(content), nil
	}

	data, err := os.ReadFile(source)
	if err != nil {
		return "", nil, fmt.Errorf("failed to read policy file %s: %w", source, err)
	}
	if len(data) > maxPolicySize {
		return "", nil, fmt.Errorf("policy file exceeds maximum size of %d bytes", maxPolicySize)
	}
	return filepath.Base(source), data, nil
}

func readArtifact(source string, maxSize int, fetchTimeout time.Duration) ([]byte, error) {
	if isURL(source) {
		return fetchRemoteArtifact(source, maxSize, fetchTimeout)
	}

	data, err := os.ReadFile(source)
	if err != nil {
		return nil, err
	}
	if len(data) > maxSize {
		return nil, fmt.Errorf("artifact exceeds maximum size of %d bytes", maxSize)
	}
	return data, nil
}

func fetchRemoteArtifact(rawURL string, maxSize int, fetchTimeout time.Duration) ([]byte, error) {
	parsed, err := url.Parse(rawURL)
	if err != nil {
		return nil, fmt.Errorf("invalid URL: %w", err)
	}

	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return nil, fmt.Errorf("unsupported URL scheme %q (only http and https are supported)", parsed.Scheme)
	}

	client := &http.Client{Timeout: fetchTimeout}
	resp, err := client.Get(rawURL)
	if err != nil {
		return nil, fmt.Errorf("HTTP request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("HTTP %d from %s", resp.StatusCode, rawURL)
	}

	limited := io.LimitReader(resp.Body, int64(maxSize)+1)
	body, err := io.ReadAll(limited)
	if err != nil {
		return nil, fmt.Errorf("failed to read response body: %w", err)
	}
	if len(body) > maxSize {
		return nil, fmt.Errorf("artifact exceeds maximum size of %d bytes", maxSize)
	}

	return body, nil
}

func resolveVerificationPublicKey(source string, fetchTimeout time.Duration) (any, error) {
	data, err := readArtifact(source, maxPolicySize, fetchTimeout)
	if err != nil {
		return nil, err
	}
	return artifactverifier.ParsePublicKeyResponse(data)
}

func publicKeyToPEM(key any) ([]byte, error) {
	switch typed := key.(type) {
	case *rsa.PublicKey, *ecdsa.PublicKey, ed25519.PublicKey:
		der, err := x509.MarshalPKIXPublicKey(typed)
		if err != nil {
			return nil, err
		}
		return pem.EncodeToMemory(&pem.Block{Type: "PUBLIC KEY", Bytes: der}), nil
	default:
		return nil, fmt.Errorf("unsupported public key type %T", key)
	}
}

// compileAndPrepare compiles rego modules and prepares the OPA query for evaluation.
func compileAndPrepare(modules map[string]string, bundleData map[string]interface{}, query string, runtimeConfig map[string]string, failOnUndefined bool) (*Evaluator, error) {
	// Compile modules to catch syntax errors early
	compiler, err := ast.CompileModulesWithOpt(modules, ast.CompileOpts{ParserOptions: ast.ParserOptions{RegoVersion: ast.RegoV1}})
	if err != nil {
		return nil, fmt.Errorf("failed to compile rego modules: %w", err)
	}

	// Build store data: merge bundle data with runtime config
	store := make(map[string]interface{})
	for k, v := range bundleData {
		store[k] = v
	}
	store["config"] = toInterfaceMap(runtimeConfig)

	pq, err := rego.New(
		rego.Query(query),
		rego.Compiler(compiler),
		rego.Store(inmem.NewFromObject(store)),
	).PrepareForEval(context.Background())
	if err != nil {
		return nil, fmt.Errorf("failed to prepare rego query %q: %w", query, err)
	}

	names := make([]string, 0, len(modules))
	for name := range modules {
		names = append(names, name)
	}

	return &Evaluator{
		preparedQuery:   pq,
		query:           query,
		runtimeConfig:   runtimeConfig,
		moduleNames:     names,
		failOnUndefined: failOnUndefined,
	}, nil
}

// isURL checks if a source string looks like a remote URL.
func isURL(source string) bool {
	return strings.HasPrefix(source, "http://") || strings.HasPrefix(source, "https://")
}

// fetchPolicy downloads a .rego file from a URL and returns (filename, content, error).
func fetchPolicy(rawURL string, fetchTimeout time.Duration) (string, string, error) {
	parsed, err := url.Parse(rawURL)
	if err != nil {
		return "", "", fmt.Errorf("invalid URL: %w", err)
	}

	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return "", "", fmt.Errorf("unsupported URL scheme %q (only http and https are supported)", parsed.Scheme)
	}

	client := &http.Client{Timeout: fetchTimeout}
	resp, err := client.Get(rawURL)
	if err != nil {
		return "", "", fmt.Errorf("HTTP request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return "", "", fmt.Errorf("HTTP %d from %s", resp.StatusCode, rawURL)
	}

	// Read with size limit
	limited := io.LimitReader(resp.Body, maxPolicySize+1)
	body, err := io.ReadAll(limited)
	if err != nil {
		return "", "", fmt.Errorf("failed to read response body: %w", err)
	}
	if len(body) > maxPolicySize {
		return "", "", fmt.Errorf("policy file exceeds maximum size of %d bytes", maxPolicySize)
	}

	// Derive filename from URL path
	name := path.Base(parsed.Path)
	if name == "" || name == "." || name == "/" {
		name = "policy.rego"
	}
	if !strings.HasSuffix(name, ".rego") {
		name += ".rego"
	}

	return name, string(body), nil
}

// Evaluate runs the compiled policy against a JSON message body.
// Returns a list of violation strings (empty = compliant).
func (e *Evaluator) Evaluate(ctx context.Context, body []byte) ([]string, error) {
	var input interface{}
	if err := json.Unmarshal(body, &input); err != nil {
		return nil, fmt.Errorf("failed to parse message body as JSON: %w", err)
	}

	rs, err := e.preparedQuery.Eval(ctx, rego.EvalInput(input))
	if err != nil {
		return nil, fmt.Errorf("rego evaluation failed: %w", err)
	}

	// Fail-closed for bundles: if the query returned no result, the policy_query_path
	// is likely misconfigured or the rule doesn't exist in the bundle.
	if e.failOnUndefined && len(rs) == 0 {
		return []string{fmt.Sprintf("policy query %q returned no result (undefined)", e.query)}, nil
	}

	return extractViolations(rs)
}

// extractViolations pulls violations from the OPA result set.
// Supported query output formats:
//   - map with {"valid": bool, "violations": []string}: structured policy_query_path result
//   - []string / set of strings: each string is a violation message
//   - bool: false = denied ("policy denied the request"), true = allowed
//   - string: non-empty = violation message
//   - empty/undefined: allowed (no violations)
func extractViolations(rs rego.ResultSet) ([]string, error) {
	if len(rs) == 0 {
		return nil, nil
	}

	var violations []string
	for _, result := range rs {
		for _, expr := range result.Expressions {
			switch v := expr.Value.(type) {
			case bool:
				// allow/deny pattern: false = denied
				if !v {
					violations = append(violations, "policy denied the request")
				}
			case string:
				// single violation string
				if v != "" {
					violations = append(violations, v)
				}
			case []interface{}:
				// Result is a list (from set)
				for _, item := range v {
					if s, ok := item.(string); ok {
						violations = append(violations, s)
					}
				}
			case map[string]interface{}:
				if vs := extractStructuredViolations(v); vs != nil {
					violations = append(violations, vs...)
				}
			}
		}
	}

	return violations, nil
}

// extractStructuredViolations handles the policy_query_path result format:
// {"valid": bool, "violations": []string}
// Returns the violation strings if the map matches this format, or nil if it doesn't.
func extractStructuredViolations(m map[string]interface{}) []string {
	validRaw, hasValid := m["valid"]
	violationsRaw, hasViolations := m["violations"]

	if !hasValid || !hasViolations {
		return nil
	}

	valid, ok := validRaw.(bool)
	if !ok {
		return nil
	}

	violationsList, ok := violationsRaw.([]interface{})
	if !ok {
		return nil
	}

	// If valid is true and violations is empty, no violations
	if valid && len(violationsList) == 0 {
		return []string{}
	}

	var violations []string
	for _, item := range violationsList {
		if s, ok := item.(string); ok {
			violations = append(violations, s)
		}
	}

	// If valid is false but violations is empty, report a generic violation
	if !valid && len(violations) == 0 {
		violations = append(violations, "policy denied the request")
	}

	return violations
}

// toInterfaceMap converts map[string]string to map[string]interface{} for OPA store.
func toInterfaceMap(m map[string]string) map[string]interface{} {
	result := make(map[string]interface{}, len(m))
	for k, v := range m {
		result[k] = v
	}
	return result
}
