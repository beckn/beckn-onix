package main

import (
	"context"
	"strings"
	"testing"

	"github.com/beckn-one/beckn-onix/pkg/plugin"
)

func TestInitCrawler_NotConfiguredIsNoOp(t *testing.T) {
	mgr := &plugin.Manager{}
	closer, err := initCrawler(context.Background(), mgr, ApplicationPlugins{})
	if err != nil {
		t.Fatal(err)
	}
	if closer == nil {
		t.Fatal("expected a no-op closer, got nil")
	}
	closer() // must not panic
}

func TestInitCrawler_RequiresRegistry(t *testing.T) {
	mgr := &plugin.Manager{}
	_, err := initCrawler(context.Background(), mgr, ApplicationPlugins{Crawler: &plugin.Config{ID: "catalogcrawler"}})
	if err == nil || !strings.Contains(err.Error(), "registry plugin") {
		t.Fatalf("err = %v, want it to name the missing registry plugin", err)
	}
}

func TestInitCrawler_UnknownRegistryPluginFailsToLoad(t *testing.T) {
	mgr := &plugin.Manager{} // no plugins loaded -- "registry" isn't found
	_, err := initCrawler(context.Background(), mgr, ApplicationPlugins{
		Crawler:  &plugin.Config{ID: "catalogcrawler"},
		Registry: &plugin.Config{ID: "registry"},
	})
	if err == nil || !strings.Contains(err.Error(), "Registry plugin") {
		t.Fatalf("err = %v, want it to fail loading the configured registry plugin", err)
	}
}
