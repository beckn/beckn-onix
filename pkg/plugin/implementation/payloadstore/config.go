package payloadstore

import (
	"fmt"
	"strconv"
	"time"
)

const (
	defaultTTL          = 24 * time.Hour
	defaultMaxBodyBytes = int64(1 << 20) // 1 MiB
)

// Config holds all configuration for the PayloadStore plugin.
// Compress is storage-level gzip applied to RequestBody/ResponseBody before writing
// to cache (reduces Redis memory usage). It is independent of HTTP Content-Encoding.
type Config struct {
	TTL            time.Duration
	IndexTTL       time.Duration
	MaxBodyBytes   int64
	StoreBody      bool
	StoreSignature bool
	Compress       bool
}

// ParseConfig parses a map[string]string config into a Config, applying defaults for absent keys.
func ParseConfig(cfg map[string]string) (*Config, error) {
	c := &Config{
		TTL:          defaultTTL,
		MaxBodyBytes: defaultMaxBodyBytes,
		StoreBody:    true,
	}

	if raw := cfg["ttl"]; raw != "" {
		d, err := time.ParseDuration(raw)
		if err != nil {
			return nil, fmt.Errorf("payloadstore: invalid ttl %q: %w", raw, err)
		}
		c.TTL = d
	}

	if c.TTL <= 0 {
		return nil, fmt.Errorf("payloadstore: ttl must be positive")
	}

	if raw := cfg["indexTTL"]; raw != "" {
		d, err := time.ParseDuration(raw)
		if err != nil {
			return nil, fmt.Errorf("payloadstore: invalid indexTTL %q: %w", raw, err)
		}
		c.IndexTTL = d
	} else {
		c.IndexTTL = c.TTL + time.Hour
	}

	if raw := cfg["maxBodyBytes"]; raw != "" {
		n, err := strconv.ParseInt(raw, 10, 64)
		if err != nil {
			return nil, fmt.Errorf("payloadstore: invalid maxBodyBytes %q: %w", raw, err)
		}
		c.MaxBodyBytes = n
	}

	if raw := cfg["storeBody"]; raw != "" {
		b, err := strconv.ParseBool(raw)
		if err != nil {
			return nil, fmt.Errorf("payloadstore: invalid storeBody %q: %w", raw, err)
		}
		c.StoreBody = b
	}

	if raw := cfg["storeSignature"]; raw != "" {
		b, err := strconv.ParseBool(raw)
		if err != nil {
			return nil, fmt.Errorf("payloadstore: invalid storeSignature %q: %w", raw, err)
		}
		c.StoreSignature = b
	}

	if raw := cfg["compress"]; raw != "" {
		b, err := strconv.ParseBool(raw)
		if err != nil {
			return nil, fmt.Errorf("payloadstore: invalid compress %q: %w", raw, err)
		}
		c.Compress = b
	}

	return c, nil
}
