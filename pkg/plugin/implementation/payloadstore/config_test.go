package payloadstore

import (
	"testing"
	"time"
)

func TestParseConfig_Defaults(t *testing.T) {
	cfg, err := ParseConfig(map[string]string{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.TTL != defaultTTL {
		t.Errorf("TTL: got %v, want %v", cfg.TTL, defaultTTL)
	}
	if cfg.IndexTTL != defaultTTL+time.Hour {
		t.Errorf("IndexTTL: got %v, want %v", cfg.IndexTTL, defaultTTL+time.Hour)
	}
	if cfg.MaxBodyBytes != defaultMaxBodyBytes {
		t.Errorf("MaxBodyBytes: got %d, want %d", cfg.MaxBodyBytes, defaultMaxBodyBytes)
	}
	if !cfg.StoreBody {
		t.Error("StoreBody: expected true by default")
	}
	if cfg.StoreSignature {
		t.Error("StoreSignature: expected false by default")
	}
	if cfg.Compress {
		t.Error("Compress: expected false by default")
	}
}

func TestParseConfig_AllFields(t *testing.T) {
	cfg, err := ParseConfig(map[string]string{
		"ttl":            "12h",
		"indexTTL":       "13h",
		"maxBodyBytes":   "2097152",
		"storeBody":      "false",
		"storeSignature": "true",
		"compress":       "true",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.TTL != 12*time.Hour {
		t.Errorf("TTL: got %v", cfg.TTL)
	}
	if cfg.IndexTTL != 13*time.Hour {
		t.Errorf("IndexTTL: got %v", cfg.IndexTTL)
	}
	if cfg.MaxBodyBytes != 2097152 {
		t.Errorf("MaxBodyBytes: got %d", cfg.MaxBodyBytes)
	}
	if cfg.StoreBody {
		t.Error("StoreBody: expected false")
	}
	if !cfg.StoreSignature {
		t.Error("StoreSignature: expected true")
	}
	if !cfg.Compress {
		t.Error("Compress: expected true")
	}
}

func TestParseConfig_InvalidTTL(t *testing.T) {
	_, err := ParseConfig(map[string]string{"ttl": "notaduration"})
	if err == nil {
		t.Error("expected error for invalid ttl")
	}
}

func TestParseConfig_ZeroTTL(t *testing.T) {
	_, err := ParseConfig(map[string]string{"ttl": "0s"})
	if err == nil {
		t.Error("expected error for zero ttl")
	}
}

func TestParseConfig_InvalidBool(t *testing.T) {
	_, err := ParseConfig(map[string]string{"storeBody": "maybe"})
	if err == nil {
		t.Error("expected error for invalid storeBody")
	}
}

func TestParseConfig_InvalidMaxBodyBytes(t *testing.T) {
	_, err := ParseConfig(map[string]string{"maxBodyBytes": "abc"})
	if err == nil {
		t.Error("expected error for invalid maxBodyBytes")
	}
}

func TestParseConfig_NegativeMaxBodyBytes(t *testing.T) {
	_, err := ParseConfig(map[string]string{"maxBodyBytes": "-1"})
	if err == nil {
		t.Error("expected error for negative maxBodyBytes")
	}
}

func TestParseConfig_ZeroMaxBodyBytes(t *testing.T) {
	cfg, err := ParseConfig(map[string]string{"maxBodyBytes": "0"})
	if err != nil {
		t.Fatalf("expected no error for maxBodyBytes=0, got: %v", err)
	}
	if cfg.MaxBodyBytes != 0 {
		t.Errorf("MaxBodyBytes: got %d, want 0", cfg.MaxBodyBytes)
	}
}

func TestParseConfig_IndexTTLShorterThanTTL(t *testing.T) {
	_, err := ParseConfig(map[string]string{
		"ttl":      "24h",
		"indexTTL": "1h",
	})
	if err == nil {
		t.Error("expected error when indexTTL < ttl")
	}
}

func TestParseConfig_IndexTTLEqualToTTL(t *testing.T) {
	_, err := ParseConfig(map[string]string{
		"ttl":      "24h",
		"indexTTL": "24h",
	})
	if err != nil {
		t.Errorf("expected no error when indexTTL == ttl, got: %v", err)
	}
}
