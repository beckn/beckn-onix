package policyenforcer

import (
	"context"
	"encoding/json"
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
	"github.com/open-policy-agent/opa/v1/rego"
	"github.com/open-policy-agent/opa/v1/storage/inmem"
)

// Evaluator wraps the OPA engine: loads and compiles .rego files at startup,
// then evaluates messages against the compiled policy set.
type Evaluator struct {
	preparedQuery rego.PreparedEvalQuery
	query         string
	runtimeConfig map[string]string
	moduleNames   []string // names of loaded .rego modules
}

// ModuleNames returns the names of the loaded .rego policy modules.
func (e *Evaluator) ModuleNames() []string {
	return e.moduleNames
}

// policyFetchTimeout is the HTTP timeout for fetching remote .rego files.
const policyFetchTimeout = 30 * time.Second

// maxPolicySize is the maximum size of a single .rego file fetched from a URL (1 MB).
const maxPolicySize = 1 << 20

// NewEvaluator creates an Evaluator by loading .rego files from local paths
// and/or URLs, then compiling them. runtimeConfig is passed to Rego as data.config.
func NewEvaluator(policyPaths, policyFile string, policyUrls []string, query string, runtimeConfig map[string]string) (*Evaluator, error) {
	modules := make(map[string]string)

	// Load from local directory
	if policyPaths != "" {
		entries, err := os.ReadDir(policyPaths)
		if err != nil {
			return nil, fmt.Errorf("failed to read policy directory %s: %w", policyPaths, err)
		}
		for _, entry := range entries {
			if entry.IsDir() {
				continue
			}
			if !strings.HasSuffix(entry.Name(), ".rego") {
				continue
			}
			// Skip test files — they shouldn't be compiled into the runtime evaluator
			if strings.HasSuffix(entry.Name(), "_test.rego") {
				continue
			}
			fpath := filepath.Join(policyPaths, entry.Name())
			data, err := os.ReadFile(fpath)
			if err != nil {
				return nil, fmt.Errorf("failed to read policy file %s: %w", fpath, err)
			}
			modules[entry.Name()] = string(data)
		}
	}

	// Load single local file
	if policyFile != "" {
		data, err := os.ReadFile(policyFile)
		if err != nil {
			return nil, fmt.Errorf("failed to read policy file %s: %w", policyFile, err)
		}
		modules[filepath.Base(policyFile)] = string(data)
	}

	// Load from URLs, local file paths, and directory paths (policyUrls)
	for _, rawSource := range policyUrls {
		if isURL(rawSource) {
			name, content, err := fetchPolicy(rawSource)
			if err != nil {
				return nil, fmt.Errorf("failed to fetch policy from %s: %w", rawSource, err)
			}
			modules[name] = content
		} else if info, err := os.Stat(rawSource); err == nil && info.IsDir() {
			// Treat as directory — load all .rego files inside
			entries, err := os.ReadDir(rawSource)
			if err != nil {
				return nil, fmt.Errorf("failed to read policy directory %s: %w", rawSource, err)
			}
			for _, entry := range entries {
				if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".rego") || strings.HasSuffix(entry.Name(), "_test.rego") {
					continue
				}
				fpath := filepath.Join(rawSource, entry.Name())
				data, err := os.ReadFile(fpath)
				if err != nil {
					return nil, fmt.Errorf("failed to read policy file %s: %w", fpath, err)
				}
				modules[entry.Name()] = string(data)
			}
		} else {
			// Treat as local file path
			data, err := os.ReadFile(rawSource)
			if err != nil {
				return nil, fmt.Errorf("failed to read local policy source %s: %w", rawSource, err)
			}
			modules[filepath.Base(rawSource)] = string(data)
		}
	}

	if len(modules) == 0 {
		return nil, fmt.Errorf("no .rego policy files found from any configured source")
	}

	// Compile modules to catch syntax errors early
	compiler, err := ast.CompileModulesWithOpt(modules, ast.CompileOpts{ParserOptions: ast.ParserOptions{RegoVersion: ast.RegoV1}})
	if err != nil {
		return nil, fmt.Errorf("failed to compile rego modules: %w", err)
	}

	// Build data.config from runtime config
	store := map[string]interface{}{
		"config": toInterfaceMap(runtimeConfig),
	}

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
		preparedQuery: pq,
		query:         query,
		runtimeConfig: runtimeConfig,
		moduleNames:   names,
	}, nil
}

// isURL checks if a source string looks like a remote URL.
func isURL(source string) bool {
	return strings.HasPrefix(source, "http://") || strings.HasPrefix(source, "https://")
}

// fetchPolicy downloads a .rego file from a URL and returns (filename, content, error).
func fetchPolicy(rawURL string) (string, string, error) {
	parsed, err := url.Parse(rawURL)
	if err != nil {
		return "", "", fmt.Errorf("invalid URL: %w", err)
	}

	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return "", "", fmt.Errorf("unsupported URL scheme %q (only http and https are supported)", parsed.Scheme)
	}

	client := &http.Client{Timeout: policyFetchTimeout}
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

	return extractViolations(rs)
}

// extractViolations pulls string violations from the OPA result set.
// The query is expected to return a set of strings.
func extractViolations(rs rego.ResultSet) ([]string, error) {
	if len(rs) == 0 {
		return nil, nil
	}

	var violations []string
	for _, result := range rs {
		for _, expr := range result.Expressions {
			switch v := expr.Value.(type) {
			case []interface{}:
				// Result is a list (from set)
				for _, item := range v {
					if s, ok := item.(string); ok {
						violations = append(violations, s)
					}
				}
			case map[string]interface{}:
				// OPA sometimes returns sets as maps with string keys
				for key := range v {
					violations = append(violations, key)
				}
			}
		}
	}

	return violations, nil
}

// toInterfaceMap converts map[string]string to map[string]interface{} for OPA store.
func toInterfaceMap(m map[string]string) map[string]interface{} {
	result := make(map[string]interface{}, len(m))
	for k, v := range m {
		result[k] = v
	}
	return result
}
