package catalogcrawler

// handler.go — the /crawl/* endpoint family's single entry point. One
// handler.Type ("catalogCrawl"), registered once, sub-routes on path
// internally rather than registering a separate handler.Type/module block
// per endpoint (trigger.go's crawl trigger, status.go's status query, and
// whatever comes next). Both today's sub-endpoints operate on the same
// already-running Crawler singleton: CrawlRegistry needs it started;
// Status works on it regardless, and duplicating the store/DB pool just to
// serve a read isn't worth a second plugin instance. A future sub-endpoint
// needing a genuinely different backing plugin would get its own named
// slot on handler.PluginCfg (parallel to Registry/KeyManager/CatalogPublisher
// already coexisting there), loaded once here and passed only to the
// sub-endpoint that needs it -- nothing about this dispatch shape requires
// every sub-endpoint to share one plugin type, only one registration.

import (
	"context"
	"fmt"
	"net/http"
	"strings"

	"github.com/beckn-one/beckn-onix/core/module"
	"github.com/beckn-one/beckn-onix/core/module/handler"
	"github.com/beckn-one/beckn-onix/pkg/log"
	"github.com/beckn-one/beckn-onix/pkg/plugin/definition"
)

// RegisterHandler wires this package's /crawl/* endpoints to the given
// already-running Crawler singleton, registering it as the Provider for
// HandlerTypeCatalogCrawl. Call this once, from main.go, right after the
// Crawler has been constructed and Start()-ed -- CrawlRegistry requires
// that exact instance to already be running, so unlike catalogpublisher's
// static handlerProviders entry (which builds everything it needs from
// PluginManager+config per module), this can't be wired ahead of time; it
// has to close over a concrete object that only exists after startup.
func RegisterHandler(crawler definition.Crawler) {
	module.RegisterProvider(handler.HandlerTypeCatalogCrawl, func(ctx context.Context, mgr handler.PluginManager, cfg *handler.Config, moduleName string) (http.Handler, error) {
		return NewHandler(ctx, crawler, cfg, moduleName)
	})
}

// NewHandler builds the /crawl/* endpoint family. Sub-routes on the request
// path stripped of cfg.BasePath: "trigger" -> the on-demand crawl trigger
// (trigger.go), "status" -> the crawl/sync status query (status.go). Both
// explicit, rather than treating the bare path as the trigger, so neither
// endpoint depends on how a bare-subtree-root request happens to redirect.
func NewHandler(ctx context.Context, crawler definition.Crawler, cfg *handler.Config, moduleName string) (http.Handler, error) {
	if crawler == nil {
		return nil, fmt.Errorf("catalogCrawl handler %s: no Crawler plugin configured/running", moduleName)
	}

	trigger := newTriggerHandler(crawler)
	status := newStatusHandler(crawler, cfg)

	log.Debugf(ctx, "catalogCrawl handler %s initialized", moduleName)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		sub := strings.TrimPrefix(strings.TrimPrefix(r.URL.Path, cfg.BasePath), "/")
		switch sub {
		case "trigger":
			trigger.ServeHTTP(w, r)
		case "status":
			status.ServeHTTP(w, r)
		default:
			http.NotFound(w, r)
		}
	}), nil
}
