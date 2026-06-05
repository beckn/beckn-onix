// Package beckndefaults loads and verifies the beckn-constants file,
// providing Beckn-defined config values that are injected into plugin
// configs at adapter startup.
package beckndefaults

import (
	"context"
	_ "embed"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/beckn-one/beckn-onix/pkg/log"
	"github.com/beckn-one/beckn-onix/pkg/security/artifactverifier"
	"gopkg.in/yaml.v2"
)

//go:embed beckn-constants.yaml
var shippedConstants []byte

//go:embed beckn-constants.yaml.sig
var shippedConstantsSig []byte

//go:embed beckn_public_key.pem
var becknPublicKeyPEM []byte

const (
	remoteConstantsURL    = "https://raw.githubusercontent.com/beckn/beckn-onix/main/pkg/beckndefaults/beckn-constants.yaml"
	remoteConstantsSigURL = "https://raw.githubusercontent.com/beckn/beckn-onix/main/pkg/beckndefaults/beckn-constants.yaml.sig"
	remoteTimeout         = 10 * time.Second
	maxConstantsFileBytes = 64 << 10 // 64 KiB — well above any realistic constants file
)

// BecknConstants is the parsed representation of beckn-constants.yaml.
type BecknConstants struct {
	Version     string                       `yaml:"becknConstantsVersion"`
	Locked      map[string]map[string]string `yaml:"locked"`
	Overridable map[string]map[string]string `yaml:"overridable"`
}

// Load fetches and verifies the beckn constants file.
// It attempts a remote refresh first unless disableRemoteRefresh is true,
// falling back to the shipped baseline on any remote failure.
func Load(ctx context.Context, disableRemoteRefresh bool) (*BecknConstants, error) {
	if !disableRemoteRefresh {
		if c, s, err := fetchRemote(ctx); err == nil {
			if bc, err := loadAndVerify(c, s); err == nil {
				log.Infof(ctx, "BecknConstants: loaded and verified from remote")
				return bc, nil
			} else {
				log.Warnf(ctx, "BecknConstants: remote copy failed verification, using shipped baseline: %v", err)
			}
		} else {
			log.Warnf(ctx, "BecknConstants: remote refresh unavailable, using shipped baseline: %v", err)
		}
	}

	bc, err := loadAndVerify(shippedConstants, shippedConstantsSig)
	if err != nil {
		return nil, fmt.Errorf("shipped beckn constants failed verification — binary may be tampered: %w", err)
	}
	log.Infof(ctx, "BecknConstants: loaded and verified from shipped baseline")
	return bc, nil
}

func loadAndVerify(constants, sig []byte) (*BecknConstants, error) {
	if err := artifactverifier.VerifyDetachedArtifact(constants, sig, becknPublicKeyPEM); err != nil {
		return nil, fmt.Errorf("signature verification: %w", err)
	}
	var bc BecknConstants
	if err := yaml.Unmarshal(constants, &bc); err != nil {
		return nil, fmt.Errorf("parse: %w", err)
	}
	return &bc, nil
}

func fetchRemote(ctx context.Context) ([]byte, []byte, error) {
	return fetchFromURLs(ctx, remoteConstantsURL, remoteConstantsSigURL)
}

// fetchFromURLs fetches the constants file and its signature from the given URLs.
// Separated from fetchRemote so tests can inject an httptest.Server URL.
func fetchFromURLs(ctx context.Context, constantsURL, sigURL string) ([]byte, []byte, error) {
	client := &http.Client{Timeout: remoteTimeout}

	fetch := func(url string) ([]byte, error) {
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
			return nil, fmt.Errorf("HTTP %d fetching %s", resp.StatusCode, url)
		}
		body, err := io.ReadAll(io.LimitReader(resp.Body, maxConstantsFileBytes+1))
		if err != nil {
			return nil, err
		}
		if len(body) > maxConstantsFileBytes {
			return nil, fmt.Errorf("response from %s exceeds maximum allowed size of %d bytes", url, maxConstantsFileBytes)
		}
		return body, nil
	}

	constants, err := fetch(constantsURL)
	if err != nil {
		return nil, nil, err
	}
	sig, err := fetch(sigURL)
	if err != nil {
		return nil, nil, err
	}
	return constants, sig, nil
}
