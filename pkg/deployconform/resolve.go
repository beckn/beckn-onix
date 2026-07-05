// Resolution of the network manifest and the deployment artifacts it
// references. Three trust paths are supported, strongest first:
//
//  1. network ID — the DeDi registry metadata is looked up, the manifest is
//     fetched and its detached signature verified (full manifestloader path);
//  2. explicit metadata URLs — manifest, signature, and key lookup URLs are
//     given directly, signature still verified;
//  3. local file — no verification; development use only.
//
// The baseline and deployment policy referenced by the manifest are then
// fetched and their own detached signatures verified with the same
// artifactverifier used for network policies.
package deployconform

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/beckn-one/beckn-onix/pkg/model"
	"github.com/beckn-one/beckn-onix/pkg/plugin/definition"
	"github.com/beckn-one/beckn-onix/pkg/plugin/implementation/dediregistry"
	"github.com/beckn-one/beckn-onix/pkg/plugin/implementation/manifestloader"
	"github.com/beckn-one/beckn-onix/pkg/security/artifactverifier"
)

// maxRemoteArtifactBytes bounds any single fetched artifact (baseline,
// policy, signature, key lookup response).
const maxRemoteArtifactBytes = 10 << 20

// ManifestSource selects how the network manifest is obtained. Exactly one
// of NetworkID, Metadata.ManifestURL, or LocalFile must be set.
type ManifestSource struct {
	// NetworkID resolves the manifest through the DeDi registry metadata for
	// "<namespace>/<registryName>".
	NetworkID string
	// DediBaseURL is the DeDi API base used with NetworkID.
	DediBaseURL string
	// Metadata fetches the manifest from explicit URLs.
	Metadata model.ManifestMetadata
	// LocalFile reads an unverified manifest from disk (development only).
	LocalFile []byte
	// SkipSignatureVerification disables manifest signature verification for
	// the NetworkID and Metadata paths. Never use it in production.
	SkipSignatureVerification bool
	// Timeout bounds each remote fetch.
	Timeout time.Duration
}

// noopRegistry satisfies the registry dependency of manifestloader for the
// explicit-metadata path, where no registry lookup can ever happen.
type noopRegistry struct{}

// LookupRegistry always fails: the explicit-metadata path never consults a
// registry, so reaching this is a programming error.
func (noopRegistry) LookupRegistry(context.Context, string, string) (*model.RegistryMetadata, error) {
	return nil, fmt.Errorf("registry lookup is not configured; provide --network-id to resolve via DeDi")
}

// ResolveManifest obtains, verifies, and parses the network manifest per the
// source configuration, returning the typed manifest. Validation binds the
// manifest to the requested network ID when one was given.
func ResolveManifest(ctx context.Context, src ManifestSource) (*model.NetworkManifest, error) {
	content, err := manifestContent(ctx, src)
	if err != nil {
		return nil, err
	}
	manifest, err := model.ParseNetworkManifest(content)
	if err != nil {
		return nil, fmt.Errorf("parse network manifest: %w", err)
	}
	expected := src.NetworkID
	if expected == "" {
		expected = manifest.NetworkID
	}
	if err := manifest.Validate(expected, time.Now().UTC()); err != nil {
		return nil, err
	}
	return manifest, nil
}

// manifestContent fetches the raw manifest bytes for the configured source,
// verifying the detached signature on the remote paths via manifestloader.
func manifestContent(ctx context.Context, src ManifestSource) ([]byte, error) {
	if len(src.LocalFile) > 0 {
		return src.LocalFile, nil
	}

	cache := newMemCache()
	loaderCfg := &manifestloader.Config{
		FetchTimeout:              src.Timeout,
		DisableCache:              true,
		SkipSignatureVerification: src.SkipSignatureVerification,
	}

	var registry definition.RegistryMetadataLookup = noopRegistry{}
	if src.NetworkID != "" {
		dedi, closer, err := dediregistry.New(ctx, cache, &dediregistry.Config{
			URL:     strings.TrimRight(src.DediBaseURL, "/"),
			Timeout: int(src.Timeout / time.Second),
		})
		if err != nil {
			return nil, fmt.Errorf("initialize DeDi registry client: %w", err)
		}
		defer func() { _ = closer() }()
		registry = dedi
	}

	loader, _, err := manifestloader.New(ctx, cache, registry, loaderCfg)
	if err != nil {
		return nil, fmt.Errorf("initialize manifest loader: %w", err)
	}

	var doc *model.ManifestDocument
	if src.NetworkID != "" {
		doc, err = loader.GetByNetworkID(ctx, src.NetworkID)
	} else {
		doc, err = loader.GetByMetadata(ctx, src.Metadata)
	}
	if err != nil {
		return nil, err
	}
	return doc.Content, nil
}

// FetchVerifiedArtifact downloads the artifact named by ref and, when the
// reference is marked signed, verifies its detached signature against the
// published public key before returning the content. skipVerify bypasses
// verification and must never be used in production.
func FetchVerifiedArtifact(ctx context.Context, client *http.Client, ref *model.NetworkManifestArtifactRef, skipVerify bool) ([]byte, error) {
	content, err := fetchBounded(ctx, client, ref.URL)
	if err != nil {
		return nil, fmt.Errorf("fetch %s: %w", ref.ID, err)
	}
	if !ref.Signed || skipVerify {
		return content, nil
	}
	signature, err := fetchBounded(ctx, client, ref.SignatureURL)
	if err != nil {
		return nil, fmt.Errorf("fetch signature for %s: %w", ref.ID, err)
	}
	publicKey, err := fetchBounded(ctx, client, ref.SigningPublicKeyLookupURL)
	if err != nil {
		return nil, fmt.Errorf("fetch public key for %s: %w", ref.ID, err)
	}
	if err := artifactverifier.VerifyDetachedArtifact(content, signature, publicKey); err != nil {
		return nil, fmt.Errorf("signature verification failed for %s: %w", ref.ID, err)
	}
	return content, nil
}

// policyFileToArtifactRef adapts a policies-section file reference to the
// shared artifact-reference shape so it can be fetched and verified by
// FetchVerifiedArtifact.
func policyFileToArtifactRef(f *model.NetworkManifestFile) *model.NetworkManifestArtifactRef {
	return &model.NetworkManifestArtifactRef{
		ID:                        f.ID,
		URL:                       f.URL,
		Signed:                    f.Signed,
		SignatureURL:              f.SignatureURL,
		SigningPublicKeyLookupURL: f.SigningPublicKeyLookupURL,
	}
}

// fetchBounded GETs a URL with a hard response-size cap.
func fetchBounded(ctx context.Context, client *http.Client, url string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("HTTP %d from %s", resp.StatusCode, url)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxRemoteArtifactBytes+1))
	if err != nil {
		return nil, err
	}
	if len(body) > maxRemoteArtifactBytes {
		return nil, fmt.Errorf("response from %s exceeds %d bytes", url, maxRemoteArtifactBytes)
	}
	return body, nil
}
