// Configuration discovery: walks the deployment graph from the devkit's
// docker-compose file. Each compose service contributes its own subtree as an
// artifact, plus every local file reachable from it — the adapter config named
// by CONFIG_FILE / --config (resolved through the service's volume mounts),
// files bind-mounted directly, and files those configs reference in turn
// (routing configs, policy configs, Rego files). All discovered paths are
// confined to the devkit root; references that resolve outside it are ignored.
package deployconform

import (
	"fmt"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
)

const (
	// maxArtifactBytes bounds a single discovered file. Devkit configuration
	// files are small; anything larger is refused rather than hashed.
	maxArtifactBytes = 2 << 20
	// maxArtifacts bounds the total number of discovered artifacts per role.
	maxArtifacts = 512
	// maxRefDepth bounds recursive file-reference chains inside config files.
	maxRefDepth = 16
)

// ArtifactKind distinguishes how an artifact is canonicalized.
type ArtifactKind string

const (
	// KindComposeService is a single service subtree of the compose file.
	KindComposeService ArtifactKind = "compose-service"
	// KindStructured is a YAML or JSON file, canonicalized as a parsed tree.
	KindStructured ArtifactKind = "structured"
	// KindRaw is any other file, hashed as LF-normalized bytes.
	KindRaw ArtifactKind = "raw"
)

// localFileRefToken replaces, before hashing, every string value that
// discovery resolved to a local file artifact. Files are identified by their
// content, not their name (see baseline.go), so the pointer to a file must
// not pin its name either — otherwise renaming a file would still deviate
// through the field that references it. URLs never resolve as local files
// and are therefore always hashed verbatim.
const localFileRefToken = "__LOCAL_FILE_REF__"

// Artifact is one discovered unit of deployed configuration, before
// redaction. Tree holds the parsed form for structured kinds; Raw holds the
// normalized bytes for raw kinds. Tokens maps string values inside Tree that
// discovery resolved to local file artifacts onto their name-neutral
// replacement (see localFileRefToken); hashing applies it so file renames do
// not change the hash of the artifact holding the reference.
type Artifact struct {
	ID     string
	Kind   ArtifactKind
	Tree   any
	Raw    []byte
	Tokens map[string]string
}

// Devkit is a loaded devkit checkout: the resolved root directory and the
// parsed compose file that discovery starts from.
type Devkit struct {
	root       string // absolute, symlink-resolved devkit root
	composeDir string // root-relative directory of the compose file (slash form)
	compose    map[string]any
	services   map[string]any
}

// LoadDevkit opens the devkit rooted at root and parses the compose file at
// composePath (root-relative). It fails when either does not exist or the
// compose file has no services mapping.
func LoadDevkit(root, composePath string) (*Devkit, error) {
	absRoot, err := filepath.Abs(root)
	if err != nil {
		return nil, fmt.Errorf("resolve devkit root: %w", err)
	}
	absRoot, err = filepath.EvalSymlinks(absRoot)
	if err != nil {
		return nil, fmt.Errorf("resolve devkit root: %w", err)
	}

	d := &Devkit{root: absRoot}
	composeRel, err := d.confine(composePath)
	if err != nil {
		return nil, fmt.Errorf("compose path: %w", err)
	}
	d.composeDir = path.Dir(composeRel)

	content, err := readBounded(filepath.Join(absRoot, filepath.FromSlash(composeRel)))
	if err != nil {
		return nil, fmt.Errorf("read compose file: %w", err)
	}
	var tree map[string]any
	if err := yaml.Unmarshal(content, &tree); err != nil {
		return nil, fmt.Errorf("parse compose file: %w", err)
	}
	services, ok := tree["services"].(map[string]any)
	if !ok || len(services) == 0 {
		return nil, fmt.Errorf("compose file %s has no services mapping", composeRel)
	}
	d.compose = tree
	d.services = services
	return d, nil
}

// ComposeTree returns the full parsed compose file, for use as policy input.
func (d *Devkit) ComposeTree() map[string]any { return d.compose }

// ServiceNames returns the sorted names of all compose services.
func (d *Devkit) ServiceNames() []string {
	names := make([]string, 0, len(d.services))
	for name := range d.services {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// HasService reports whether the compose file declares the named service.
func (d *Devkit) HasService(name string) bool {
	_, ok := d.services[name]
	return ok
}

// RoleArtifacts discovers every artifact belonging to a role: one
// compose-service artifact per declared service that exists locally, every
// file reachable from those services, and any extra files matched by the
// include globs. Declared services missing from the local compose file are
// simply absent from the result — verification reports them as missing.
func (d *Devkit) RoleArtifacts(services, include []string) ([]Artifact, error) {
	byID := make(map[string]Artifact)
	for _, name := range services {
		subtree, ok := d.services[name]
		if !ok {
			continue
		}
		byID[composeArtifactPrefix+name] = Artifact{
			ID:   composeArtifactPrefix + name,
			Kind: KindComposeService,
			Tree: subtree,
		}
		if err := d.discoverServiceFiles(name, byID); err != nil {
			return nil, err
		}
	}
	if err := d.discoverIncluded(include, byID); err != nil {
		return nil, err
	}

	ids := make([]string, 0, len(byID))
	for id := range byID {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	artifacts := make([]Artifact, 0, len(ids))
	for _, id := range ids {
		artifacts = append(artifacts, byID[id])
	}
	return artifacts, nil
}

// discoverServiceFiles adds every file reachable from one compose service:
// bind-mounted files, and the adapter config named by the CONFIG_FILE
// environment variable or a --config=<path> command argument (both are
// container paths, translated to host paths through the service's mounts).
// Every string in the service subtree that resolved to a file artifact is
// recorded in the compose artifact's token map, so the service definition
// hashes independently of the referenced file names.
func (d *Devkit) discoverServiceFiles(name string, byID map[string]Artifact) error {
	service, _ := d.services[name].(map[string]any)
	mounts := d.serviceMounts(service)
	tokens := make(map[string]string)

	for _, m := range mounts {
		abs := filepath.Join(d.root, filepath.FromSlash(m.hostRel))
		if info, err := os.Stat(abs); err == nil && info.Mode().IsRegular() {
			if err := d.addFile(m.hostRel, byID, 0); err != nil {
				return err
			}
			if _, added := byID[m.hostRel]; added {
				tokens[m.tokenKey] = m.tokenValue
			}
		}
	}

	for _, containerPath := range configEntrypoints(service) {
		hostRel, ok := containerToHost(mounts, containerPath)
		if !ok {
			continue // path lives inside the image, not in the devkit
		}
		if err := d.addFile(hostRel, byID, 0); err != nil {
			return err
		}
		if _, added := byID[hostRel]; added {
			// Cover every syntactic site the container path can appear in:
			// environment map value, "CONFIG_FILE=…" list entry, and both
			// --config argument forms.
			tokens[containerPath] = localFileRefToken
			tokens["CONFIG_FILE="+containerPath] = "CONFIG_FILE=" + localFileRefToken
			tokens["--config="+containerPath] = "--config=" + localFileRefToken
		}
	}

	if len(tokens) > 0 {
		artifact := byID[composeArtifactPrefix+name]
		artifact.Tokens = tokens
		byID[composeArtifactPrefix+name] = artifact
	}
	return nil
}

// addFile loads the root-relative file rel as an artifact and, for structured
// files, recursively follows relative path references found in string values.
// Unresolvable references (paths that only exist inside the container image,
// or that escape the devkit root) are skipped, not errors.
func (d *Devkit) addFile(rel string, byID map[string]Artifact, depth int) error {
	if depth > maxRefDepth {
		return fmt.Errorf("reference chain exceeds depth %d at %s", maxRefDepth, rel)
	}
	if _, seen := byID[rel]; seen {
		return nil
	}
	if len(byID) >= maxArtifacts {
		return fmt.Errorf("discovered more than %d artifacts; refusing to continue", maxArtifacts)
	}

	content, err := readBounded(filepath.Join(d.root, filepath.FromSlash(rel)))
	if err != nil {
		return fmt.Errorf("read artifact %s: %w", rel, err)
	}

	if !isStructuredFile(rel) {
		byID[rel] = Artifact{ID: rel, Kind: KindRaw, Raw: normalizeRaw(content)}
		return nil
	}

	var tree any
	if err := yaml.Unmarshal(content, &tree); err != nil {
		return fmt.Errorf("parse artifact %s: %w", rel, err)
	}
	byID[rel] = Artifact{ID: rel, Kind: KindStructured, Tree: tree}

	tokens := make(map[string]string)
	for _, ref := range collectPathRefs(tree) {
		refRel, err := d.confine(ref)
		if err != nil {
			continue
		}
		abs := filepath.Join(d.root, filepath.FromSlash(refRel))
		if info, statErr := os.Stat(abs); statErr != nil || !info.Mode().IsRegular() {
			continue
		}
		if err := d.addFile(refRel, byID, depth+1); err != nil {
			return err
		}
		tokens[ref] = localFileRefToken
	}
	if len(tokens) > 0 {
		artifact := byID[rel]
		artifact.Tokens = tokens
		byID[rel] = artifact
	}
	return nil
}

// discoverIncluded adds files matched by the baseline's include globs.
// Supported forms: a standard path.Match pattern against the root-relative
// path, or "<dir>/**" meaning every file under dir.
func (d *Devkit) discoverIncluded(include []string, byID map[string]Artifact) error {
	if len(include) == 0 {
		return nil
	}
	return filepath.WalkDir(d.root, func(p string, entry fs.DirEntry, err error) error {
		if err != nil || !entry.Type().IsRegular() {
			return err
		}
		rel, err := filepath.Rel(d.root, p)
		if err != nil {
			return err
		}
		rel = filepath.ToSlash(rel)
		for _, glob := range include {
			if matchInclude(glob, rel) {
				return d.addFile(rel, byID, 0)
			}
		}
		return nil
	})
}

// matchInclude reports whether the root-relative path rel matches one include
// glob: "<dir>/**" matches everything under dir, anything else is a
// path.Match pattern.
func matchInclude(glob, rel string) bool {
	if prefix, ok := strings.CutSuffix(glob, "/**"); ok {
		return rel == prefix || strings.HasPrefix(rel, prefix+"/")
	}
	ok, err := path.Match(glob, rel)
	return err == nil && ok
}

// confine resolves p — either root-relative or relative to the current
// working directory of the devkit convention ("./config/x.yaml" style) — to a
// cleaned root-relative slash path, rejecting anything that escapes the
// devkit root (including via symlinks).
func (d *Devkit) confine(p string) (string, error) {
	joined := filepath.Join(d.root, filepath.FromSlash(p))
	resolved, err := filepath.EvalSymlinks(joined)
	if err != nil {
		// The path may not exist yet (e.g. compose path typo); confine the
		// lexical form so the caller gets a clean not-found error later.
		resolved = filepath.Clean(joined)
	}
	rel, err := filepath.Rel(d.root, resolved)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("path %q escapes devkit root", p)
	}
	return filepath.ToSlash(rel), nil
}

// mount is one bind mount of a compose service, with the host side already
// resolved to a root-relative slash path. tokenKey is the original string
// value carrying the host path in the compose tree; tokenValue is that value
// with the host path replaced by localFileRefToken — applied only when the
// mounted file becomes an artifact, so file bind mounts hash independently
// of the file's name.
type mount struct {
	hostRel    string
	container  string
	tokenKey   string
	tokenValue string
}

// serviceMounts parses the service's volumes into bind mounts. Named volumes
// and absolute host paths are skipped: only paths inside the devkit checkout
// can be verified. Both the short "host:container[:mode]" string form and the
// long map form (source/target) are supported.
func (d *Devkit) serviceMounts(service map[string]any) []mount {
	volumes, _ := service["volumes"].([]any)
	var mounts []mount
	for _, v := range volumes {
		var host, container, tokenKey, tokenValue string
		switch t := v.(type) {
		case string:
			parts := strings.SplitN(t, ":", 3)
			if len(parts) < 2 {
				continue
			}
			host, container = parts[0], parts[1]
			tokenKey = t
			tokenValue = localFileRefToken + strings.TrimPrefix(t, host)
		case map[string]any:
			host, _ = t["source"].(string)
			container, _ = t["target"].(string)
			tokenKey = host
			tokenValue = localFileRefToken
		}
		if !strings.HasPrefix(host, "./") && !strings.HasPrefix(host, "../") {
			continue // named volume or absolute path
		}
		// Compose resolves relative host paths against the compose file's
		// directory, so confine them from there.
		hostRel, err := d.confine(path.Join(d.composeDir, host))
		if err != nil {
			continue
		}
		mounts = append(mounts, mount{hostRel: hostRel, container: path.Clean(container), tokenKey: tokenKey, tokenValue: tokenValue})
	}
	return mounts
}

// configEntrypoints extracts the container paths of adapter configuration
// files from a service definition: the CONFIG_FILE environment variable and
// any --config=<path> or --config <path> command argument.
func configEntrypoints(service map[string]any) []string {
	var paths []string
	if v := envValue(service, "CONFIG_FILE"); v != "" {
		paths = append(paths, v)
	}
	var args []string
	switch cmd := service["command"].(type) {
	case []any:
		for _, arg := range cmd {
			if s, ok := arg.(string); ok {
				args = append(args, s)
			}
		}
	case string:
		args = strings.Fields(cmd)
	}
	for i, arg := range args {
		if p, found := strings.CutPrefix(arg, "--config="); found {
			paths = append(paths, p)
		} else if arg == "--config" && i+1 < len(args) {
			paths = append(paths, args[i+1])
		}
	}
	return paths
}

// envValue reads one variable from a service's environment section, which
// compose allows either as a mapping or as a list of "KEY=VALUE" strings.
func envValue(service map[string]any, key string) string {
	switch env := service["environment"].(type) {
	case map[string]any:
		if v, ok := env[key].(string); ok {
			return v
		}
	case []any:
		for _, item := range env {
			s, ok := item.(string)
			if !ok {
				continue
			}
			if v, found := strings.CutPrefix(s, key+"="); found {
				return v
			}
		}
	}
	return ""
}

// containerToHost translates a container path to a root-relative host path
// through the service's bind mounts, choosing the longest matching mount
// prefix. The second result is false when no mount covers the path.
func containerToHost(mounts []mount, containerPath string) (string, bool) {
	containerPath = path.Clean(containerPath)
	best := -1
	for i, m := range mounts {
		if containerPath != m.container && !strings.HasPrefix(containerPath, m.container+"/") {
			continue
		}
		if best == -1 || len(m.container) > len(mounts[best].container) {
			best = i
		}
	}
	if best == -1 {
		return "", false
	}
	m := mounts[best]
	if containerPath == m.container {
		return m.hostRel, true
	}
	return path.Join(m.hostRel, strings.TrimPrefix(containerPath, m.container+"/")), true
}

// collectPathRefs gathers every string value in a parsed tree that looks like
// a relative reference to a configuration file ("./x" or "../x" ending in a
// config-bearing extension). By devkit convention these are resolved against
// the devkit root. The extension allowlist keeps the heuristic from pulling
// arbitrary referenced files — data files, key material — into the baseline;
// anything beyond it must be pinned explicitly with include globs.
func collectPathRefs(tree any) []string {
	var refs []string
	walkStrings(tree, func(s string) {
		if !strings.HasPrefix(s, "./") && !strings.HasPrefix(s, "../") {
			return
		}
		switch strings.ToLower(path.Ext(s)) {
		case ".yaml", ".yml", ".json", ".rego":
			refs = append(refs, s)
		}
	})
	return refs
}

// walkStrings visits every string value in a parsed YAML/JSON tree.
func walkStrings(tree any, visit func(string)) {
	switch t := tree.(type) {
	case string:
		visit(t)
	case []any:
		for _, item := range t {
			walkStrings(item, visit)
		}
	case map[string]any:
		for _, item := range t {
			walkStrings(item, visit)
		}
	}
}

// isStructuredFile reports whether the file is parsed as YAML/JSON for
// canonicalization, based on its extension.
func isStructuredFile(rel string) bool {
	switch strings.ToLower(path.Ext(rel)) {
	case ".yaml", ".yml", ".json":
		return true
	}
	return false
}

// readBounded reads a file, refusing anything larger than maxArtifactBytes.
func readBounded(abs string) ([]byte, error) {
	info, err := os.Stat(abs)
	if err != nil {
		return nil, err
	}
	if info.Size() > maxArtifactBytes {
		return nil, fmt.Errorf("file %s exceeds %d bytes", abs, maxArtifactBytes)
	}
	return os.ReadFile(abs)
}
