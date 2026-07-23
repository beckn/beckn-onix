package deployconform

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// renameFile renames a devkit file and rewrites every occurrence of oldRef
// to newRef in the referencing file, mimicking what a participant does when
// renaming a config.
func renameFile(t *testing.T, root, oldPath, newPath, referencer, oldRef, newRef string) {
	t.Helper()
	if err := os.Rename(filepath.Join(root, filepath.FromSlash(oldPath)), filepath.Join(root, filepath.FromSlash(newPath))); err != nil {
		t.Fatal(err)
	}
	refFile := filepath.Join(root, filepath.FromSlash(referencer))
	content, err := os.ReadFile(refFile)
	if err != nil {
		t.Fatal(err)
	}
	edited := strings.ReplaceAll(string(content), oldRef, newRef)
	if edited == string(content) {
		t.Fatalf("reference %q not found in %s", oldRef, referencer)
	}
	if err := os.WriteFile(refFile, []byte(edited), 0o644); err != nil {
		t.Fatal(err)
	}
}

// verifyAlpha loads the devkit at root and verifies the alpha role.
func verifyAlpha(t *testing.T, baseline *Baseline, root string) *Report {
	t.Helper()
	devkit, err := LoadDevkit(root, "install/docker-compose.yml")
	if err != nil {
		t.Fatal(err)
	}
	report, err := VerifyRole(devkit, baseline, "alpha")
	if err != nil {
		t.Fatal(err)
	}
	return report
}

// requireRename asserts a compliant report carrying exactly one rename with
// matching root hashes.
func requireRename(t *testing.T, report *Report, from, to string) {
	t.Helper()
	if !report.Compliant() {
		t.Fatalf("rename must stay compliant, got findings %+v", report.Findings)
	}
	if report.ExpectedRoot != report.ComputedRoot {
		t.Fatalf("rename-only checkout must reproduce the baseline root (expected %s, computed %s)", report.ExpectedRoot, report.ComputedRoot)
	}
	if len(report.Renames) != 1 || report.Renames[0] != (Rename{From: from, To: to}) {
		t.Fatalf("renames = %+v, want [{%s %s}]", report.Renames, from, to)
	}
}

// TestVerifyRoleRenamedFile renames a wholly participant-owned file (the
// routing config) into a different directory and updates the pointer in the
// adapter config: content identity makes this fully compliant.
func TestVerifyRoleRenamedFile(t *testing.T) {
	baseline := generateTestBaseline(t)
	root := copyDevkit(t)

	renameFile(t, root,
		"config/routing-alpha.yaml", "policies/my-routes.yaml",
		"config/adapter-alpha.yaml",
		"./config/routing-alpha.yaml", "./policies/my-routes.yaml")

	report := verifyAlpha(t, baseline, root)
	requireRename(t, report, "config/routing-alpha.yaml", "policies/my-routes.yaml")
}

// TestVerifyRoleRenamedRawFile renames a network-fixed raw file (the Rego
// policy) and updates the pointer in the policy config: still compliant,
// because the content is byte-identical.
func TestVerifyRoleRenamedRawFile(t *testing.T) {
	baseline := generateTestBaseline(t)
	root := copyDevkit(t)

	renameFile(t, root,
		"policies/network.rego", "policies/net.rego",
		"config/opa-policies.yaml",
		"./policies/network.rego", "./policies/net.rego")

	report := verifyAlpha(t, baseline, root)
	requireRename(t, report, "policies/network.rego", "policies/net.rego")
}

// TestVerifyRoleRenamedOutOfVarianceGlob renames the adapter config itself.
// The new name no longer matches the "config/adapter-*.yaml" variance glob,
// so the slot-profile retry is what makes the match: the file is re-hashed
// under its baseline slot's profile. The pointers live in the compose
// service (CONFIG_FILE and --config=), exercising compose-side tokens.
func TestVerifyRoleRenamedOutOfVarianceGlob(t *testing.T) {
	baseline := generateTestBaseline(t)
	root := copyDevkit(t)

	renameFile(t, root,
		"config/adapter-alpha.yaml", "config/main-config.yaml",
		"install/docker-compose.yml",
		"/app/config/adapter-alpha.yaml", "/app/config/main-config.yaml")

	report := verifyAlpha(t, baseline, root)
	requireRename(t, report, "config/adapter-alpha.yaml", "config/main-config.yaml")
}

// TestVerifyRoleRenamedAndModified renames a network-fixed file AND changes
// its content: no slot reproduces the baseline hash, so it is reported as
// missing plus unexpected — a rename is only free when content conforms.
func TestVerifyRoleRenamedAndModified(t *testing.T) {
	baseline := generateTestBaseline(t)
	root := copyDevkit(t)

	renameFile(t, root,
		"policies/network.rego", "policies/net.rego",
		"config/opa-policies.yaml",
		"./policies/network.rego", "./policies/net.rego")
	rego := filepath.Join(root, "policies", "net.rego")
	content, _ := os.ReadFile(rego)
	if err := os.WriteFile(rego, append(content, []byte("\n# tampered\n")...), 0o644); err != nil {
		t.Fatal(err)
	}

	report := verifyAlpha(t, baseline, root)
	if report.Compliant() {
		t.Fatal("rename+modify must not be compliant")
	}
	if len(report.Renames) != 0 {
		t.Fatalf("no rename should be recorded, got %+v", report.Renames)
	}
	missing, ok := findingByKind(report, FindingMissing)
	if !ok || missing.ArtifactID != "policies/network.rego" {
		t.Fatalf("expected missing finding for network.rego, got %+v", report.Findings)
	}
	unexpected, ok := findingByKind(report, FindingUnexpected)
	if !ok || unexpected.ArtifactID != "policies/net.rego" {
		t.Fatalf("expected unexpected finding for net.rego, got %+v", report.Findings)
	}
}

// TestHashArtifactTokens checks the reference-token mechanics directly:
// resolved file references are neutralized, URLs and unresolved strings are
// hashed verbatim, and the original tree is never mutated.
func TestHashArtifactTokens(t *testing.T) {
	tree := map[string]any{
		"ref": "./config/y.yaml",
		"url": "https://example.com/policy.rego",
	}
	artifact := Artifact{
		ID:     "config/x.yaml",
		Kind:   KindStructured,
		Tree:   tree,
		Tokens: map[string]string{"./config/y.yaml": localFileRefToken},
	}

	hashed, err := hashArtifact(artifact, nil, DefaultPlaceholder)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(hashed.Canonical, localFileRefToken) || strings.Contains(hashed.Canonical, "./config/y.yaml") {
		t.Fatalf("resolved reference not tokenized: %s", hashed.Canonical)
	}
	if !strings.Contains(hashed.Canonical, "https://example.com/policy.rego") {
		t.Fatalf("URL must be hashed verbatim: %s", hashed.Canonical)
	}
	if tree["ref"] != "./config/y.yaml" {
		t.Fatal("hashArtifact must not mutate the original tree")
	}

	// Renaming the referenced file (a different token key, same token value)
	// must not change the hash.
	renamed := artifact
	renamed.Tree = map[string]any{
		"ref": "./config/z.yaml",
		"url": "https://example.com/policy.rego",
	}
	renamed.Tokens = map[string]string{"./config/z.yaml": localFileRefToken}
	rehashed, err := hashArtifact(renamed, nil, DefaultPlaceholder)
	if err != nil {
		t.Fatal(err)
	}
	if rehashed.SHA256 != hashed.SHA256 {
		t.Fatal("hash must be independent of the referenced file's name")
	}
}
