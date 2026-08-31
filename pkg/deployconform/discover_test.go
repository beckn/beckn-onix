package deployconform

import (
	"path/filepath"
	"strings"
	"testing"
)

// testDevkit loads the miniature devkit under testdata.
func testDevkit(t *testing.T) *Devkit {
	t.Helper()
	devkit, err := LoadDevkit(filepath.Join("testdata", "devkit"), "install/docker-compose.yml")
	if err != nil {
		t.Fatalf("LoadDevkit: %v", err)
	}
	return devkit
}

// artifactIDs extracts the sorted IDs of a discovery result.
func artifactIDs(artifacts []Artifact) []string {
	ids := make([]string, 0, len(artifacts))
	for _, a := range artifacts {
		ids = append(ids, a.ID)
	}
	return ids
}

// TestLoadDevkit verifies compose parsing and error cases.
func TestLoadDevkit(t *testing.T) {
	devkit := testDevkit(t)
	want := []string{"onix-alpha", "onix-beta", "redis-alpha"}
	got := devkit.ServiceNames()
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("ServiceNames = %v, want %v", got, want)
	}
	if !devkit.HasService("onix-alpha") || devkit.HasService("nope") {
		t.Fatalf("HasService gave wrong answers")
	}

	if _, err := LoadDevkit(filepath.Join("testdata", "devkit"), "install/missing.yml"); err == nil {
		t.Fatalf("expected error for missing compose file")
	}
	if _, err := LoadDevkit(filepath.Join("testdata", "devkit"), "../devkit/install/docker-compose.yml"); err != nil {
		t.Fatalf("clean in-root path should load, got %v", err)
	}
	if _, err := LoadDevkit(filepath.Join("testdata", "devkit"), "../../canonical.go"); err == nil {
		t.Fatalf("expected error for compose path escaping the root")
	}
}

// TestRoleArtifactsAlpha checks the full discovery chain for the alpha role:
// compose subtrees, CONFIG_FILE translation through mounts, recursive
// references, raw bind-mounted files, and skipped unresolvable references.
func TestRoleArtifactsAlpha(t *testing.T) {
	devkit := testDevkit(t)
	artifacts, err := devkit.RoleArtifacts([]string{"onix-alpha", "redis-alpha"}, nil)
	if err != nil {
		t.Fatalf("RoleArtifacts: %v", err)
	}

	want := []string{
		"compose:onix-alpha",
		"compose:redis-alpha",
		"config/adapter-alpha.yaml",
		"config/opa-policies.yaml",
		"config/routing-alpha.yaml",
		"install/startup.js",
		"policies/network.rego",
	}
	got := artifactIDs(artifacts)
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("artifact IDs = %v, want %v", got, want)
	}

	kinds := map[string]ArtifactKind{}
	for _, a := range artifacts {
		kinds[a.ID] = a.Kind
	}
	if kinds["compose:onix-alpha"] != KindComposeService {
		t.Errorf("compose:onix-alpha kind = %s", kinds["compose:onix-alpha"])
	}
	if kinds["config/adapter-alpha.yaml"] != KindStructured {
		t.Errorf("adapter-alpha kind = %s", kinds["config/adapter-alpha.yaml"])
	}
	if kinds["policies/network.rego"] != KindRaw {
		t.Errorf("network.rego kind = %s", kinds["policies/network.rego"])
	}
	if kinds["install/startup.js"] != KindRaw {
		t.Errorf("startup.js kind = %s", kinds["install/startup.js"])
	}
}

// TestRoleArtifactsBeta checks list-form environment CONFIG_FILE extraction
// and that a missing declared service is silently absent.
func TestRoleArtifactsBeta(t *testing.T) {
	devkit := testDevkit(t)
	artifacts, err := devkit.RoleArtifacts([]string{"onix-beta", "not-deployed"}, nil)
	if err != nil {
		t.Fatalf("RoleArtifacts: %v", err)
	}
	want := []string{"compose:onix-beta", "config/adapter-beta.yaml"}
	got := artifactIDs(artifacts)
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("artifact IDs = %v, want %v", got, want)
	}
}

// TestRoleArtifactsInclude checks the include-glob forms.
func TestRoleArtifactsInclude(t *testing.T) {
	devkit := testDevkit(t)
	tests := []struct {
		name    string
		include []string
		wantID  string
	}{
		{name: "dir glob", include: []string{"schemas/**"}, wantID: "schemas/extra.json"},
		{name: "path.Match glob", include: []string{"schemas/*.json"}, wantID: "schemas/extra.json"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			artifacts, err := devkit.RoleArtifacts([]string{"redis-alpha"}, tt.include)
			if err != nil {
				t.Fatalf("RoleArtifacts: %v", err)
			}
			found := false
			for _, id := range artifactIDs(artifacts) {
				if id == tt.wantID {
					found = true
				}
			}
			if !found {
				t.Fatalf("include %v did not add %s (got %v)", tt.include, tt.wantID, artifactIDs(artifacts))
			}
		})
	}
}

// TestContainerToHost checks mount translation, including longest-prefix
// selection and unmapped paths.
func TestContainerToHost(t *testing.T) {
	mounts := []mount{
		{hostRel: "config", container: "/app/config"},
		{hostRel: "config/special", container: "/app/config/special"},
		{hostRel: "policies", container: "/app/policies"},
	}
	tests := []struct {
		name      string
		container string
		want      string
		wantOK    bool
	}{
		{name: "direct child", container: "/app/config/x.yaml", want: "config/x.yaml", wantOK: true},
		{name: "longest prefix wins", container: "/app/config/special/y.yaml", want: "config/special/y.yaml", wantOK: true},
		{name: "exact mount", container: "/app/policies", want: "policies", wantOK: true},
		{name: "inside image", container: "/etc/passwd", wantOK: false},
		{name: "prefix but not path boundary", container: "/app/configuration/x", wantOK: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := containerToHost(mounts, tt.container)
			if ok != tt.wantOK || got != tt.want {
				t.Fatalf("containerToHost(%q) = (%q, %v), want (%q, %v)", tt.container, got, ok, tt.want, tt.wantOK)
			}
		})
	}
}

// TestMatchInclude checks both supported include-glob forms.
func TestMatchInclude(t *testing.T) {
	tests := []struct {
		glob string
		rel  string
		want bool
	}{
		{"schemas/**", "schemas/a/b.json", true},
		{"schemas/**", "schemas", true},
		{"schemas/**", "schemas2/x.json", false},
		{"schemas/*.json", "schemas/x.json", true},
		{"schemas/*.json", "schemas/sub/x.json", false},
		{"[bad", "anything", false},
	}
	for _, tt := range tests {
		if got := matchInclude(tt.glob, tt.rel); got != tt.want {
			t.Errorf("matchInclude(%q, %q) = %v, want %v", tt.glob, tt.rel, got, tt.want)
		}
	}
}

// TestConfigEntrypoints checks CONFIG_FILE (map and list forms) and
// --config= extraction from list and string commands.
func TestConfigEntrypoints(t *testing.T) {
	tests := []struct {
		name    string
		service map[string]any
		want    []string
	}{
		{
			name: "map env and list command",
			service: map[string]any{
				"environment": map[string]any{"CONFIG_FILE": "/app/config/a.yaml"},
				"command":     []any{"./server", "--config=/app/config/b.yaml"},
			},
			want: []string{"/app/config/a.yaml", "/app/config/b.yaml"},
		},
		{
			name: "list env",
			service: map[string]any{
				"environment": []any{"NODE_ENV=production", "CONFIG_FILE=/app/config/c.yaml"},
			},
			want: []string{"/app/config/c.yaml"},
		},
		{
			name: "string command",
			service: map[string]any{
				"command": "./server --config=/app/config/d.yaml",
			},
			want: []string{"/app/config/d.yaml"},
		},
		{
			name:    "nothing",
			service: map[string]any{},
			want:    nil,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := configEntrypoints(tt.service)
			if strings.Join(got, ",") != strings.Join(tt.want, ",") {
				t.Fatalf("configEntrypoints = %v, want %v", got, tt.want)
			}
		})
	}
}
