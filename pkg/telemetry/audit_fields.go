package telemetry

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/beckn-one/beckn-onix/pkg/log"
	"gopkg.in/yaml.v3"
)

type piiPatternDef struct {
	Name     string `yaml:"name"`
	Regex    string `yaml:"regex"`
	MaskType string `yaml:"maskType"` // "replace" (default) or "last4"
	Mask     string `yaml:"mask"`     // literal mask for maskType "replace"
}

type piiPathDef struct {
	Path    string `yaml:"path"`
	Pattern string `yaml:"pattern"`
}

type auditConfig struct {
	PIIPatterns []piiPatternDef `yaml:"piiPatterns"`
	PIIPaths    []piiPathDef    `yaml:"piiPaths"`
}

type CompiledPattern struct {
	Name     string
	Re       *regexp.Regexp
	MaskType string // "replace" or "last4"
	Mask     string
}

type CompiledPIIConfig struct {
	Patterns map[string]*CompiledPattern
	Paths    []piiPathDef
}

var (
	piiConfig   *CompiledPIIConfig
	piiConfigMu sync.RWMutex
)

func loadAuditConfig(ctx context.Context, source string) error {
	str := strings.TrimSpace(source)
	if str == "" {
		err := fmt.Errorf("auditFieldsConfig is empty")
		log.Error(ctx, err, "audit config source is empty")
		return err
	}

	var data []byte
	u, err := url.Parse(str)
	if err == nil && (u.Scheme == "http" || u.Scheme == "https") {
		resp, err := http.Get(str)
		if err != nil {
			log.Error(ctx, err, "failed to fetch audit config from url")
			return err
		}
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			err := fmt.Errorf("unexpected status %d fetching audit config from %s", resp.StatusCode, str)
			log.Error(ctx, err, "failed to fetch audit config from url")
			return err
		}
		data, err = io.ReadAll(resp.Body)
		if err != nil {
			log.Error(ctx, err, "failed to read audit config from url")
			return err
		}
	} else {
		data, err = os.ReadFile(str)
		if err != nil {
			log.Error(ctx, err, "failed to read audit config file")
			return err
		}
	}

	var config auditConfig
	if err := yaml.Unmarshal(data, &config); err != nil {
		log.Error(ctx, err, "failed to parse audit config file")
		return err
	}

	compiled := &CompiledPIIConfig{
		Patterns: make(map[string]*CompiledPattern, len(config.PIIPatterns)),
		Paths:    config.PIIPaths,
	}
	for _, p := range config.PIIPatterns {
		re, err := regexp.Compile(p.Regex)
		if err != nil {
			log.Error(ctx, err, fmt.Sprintf("invalid regex for piiPattern %q", p.Name))
			return err
		}
		maskType := p.MaskType
		if maskType == "" {
			maskType = "replace"
		}
		compiled.Patterns[p.Name] = &CompiledPattern{
			Name:     p.Name,
			Re:       re,
			MaskType: maskType,
			Mask:     p.Mask,
		}
	}

	piiConfigMu.Lock()
	piiConfig = compiled
	piiConfigMu.Unlock()
	log.Info(ctx, "audit config loaded (piiPatterns + piiPaths)")
	return nil
}

// LoadAuditConfig loads audit config (PII patterns + paths) from a single source:
// either an HTTP(S) URL or a local file path.
func LoadAuditConfig(ctx context.Context, configPath string) error {
	return loadAuditConfig(ctx, configPath)
}

// GetPIIConfig returns the current compiled PII config (patterns + paths).
func GetPIIConfig() *CompiledPIIConfig {
	piiConfigMu.RLock()
	defer piiConfigMu.RUnlock()
	return piiConfig
}

// StartAuditFieldsRefresh periodically reloads audit config from the configured source (URL or local path).
func StartAuditFieldsRefresh(ctx context.Context, configSource string, intervalSec int64) (stop func()) {
	if intervalSec <= 0 {
		intervalSec = 3600
	}
	interval := time.Duration(intervalSec) * time.Second
	ticker := time.NewTicker(interval)
	done := make(chan struct{})

	if err := loadAuditConfig(ctx, configSource); err != nil {
		log.Warn(ctx, "failed to load audit config")
	}

	go func() {
		for {
			select {
			case <-done:
				ticker.Stop()
				return
			case <-ticker.C:
				reloadCtx := context.Background()
				if err := loadAuditConfig(reloadCtx, configSource); err != nil {
					log.Warn(reloadCtx, "failed to reload audit config")
				}
			}
		}
	}()

	return func() { close(done) }
}
