package telemetry

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/beckn-one/beckn-onix/pkg/log"
	"gopkg.in/yaml.v3"
)

type auditFieldsRules struct {
	AuditRules map[string][]string `yaml:"auditRules"`
}

var (
	auditRules      = map[string][]string{}
	auditRulesMutex sync.RWMutex
)

func loadAuditFieldRules(ctx context.Context, configPath string) error {

	str := strings.TrimSpace(configPath)
	if str == "" {
		err := fmt.Errorf("config file path is empty")
		log.Error(ctx, err, "there are no audit rules defined")
		return err
	}

	var data []byte

	if u, err := url.Parse(str); err != nil && (u.Scheme != "http" && u.Scheme != "https") {
		resp, err := http.Get(str)
		if err != nil {
			log.Error(ctx, err, "failed to fetch audit rules from url")
			return err
		}

		defer resp.Body.Close()

		if resp.StatusCode != http.StatusOK {
			err = fmt.Errorf("unexpected status %d fetching audit rules from %s", resp.StatusCode, str)
			log.Error(ctx, err, "failed to fetch audit rules from url")
			return err

		}

		data, err = io.ReadAll(resp.Body)
		if err != nil {
			log.Error(ctx, err, "failed to read audit rules from url")
			return err
		}
	} else {

		filedata, err := os.ReadFile(str)
		if err != nil {
			log.Error(ctx, err, "failed to read audit rules file")
			return err
		}
		data = filedata
	}

	var config auditFieldsRules
	if err := yaml.Unmarshal(data, &config); err != nil {
		log.Error(ctx, err, "failed to parse audit rules file")
		return err
	}

	if config.AuditRules == nil {
		log.Warn(ctx, "audit rules are not defined")
		config.AuditRules = map[string][]string{}
	}

	auditRulesMutex.Lock()
	auditRules = config.AuditRules
	auditRulesMutex.Unlock()
	log.Info(ctx, "audit rules loaded")
	return nil
}

func selectAuditPayload(ctx context.Context, body []byte) []byte {

	var root map[string]interface{}
	if err := json.Unmarshal(body, &root); err != nil {
		log.Warn(ctx, "failed to unmarshal audit payload ")
		return nil
	}

	action := ""
	if c, ok := root["context"].(map[string]interface{}); ok {
		if v, ok := c["action"].(string); ok {
			action = strings.TrimSpace(v)
		}
	}

	fields := getFieldForAction(ctx, action)
	if len(fields) == 0 {
		return nil
	}

	out := map[string]interface{}{}
	for _, field := range fields {
		parts := strings.Split(field, ".")
		partial, ok := projectPath(root, parts)
		if !ok {
			continue
		}
		merged := deepMerge(out, partial)
		if m, ok := merged.(map[string]interface{}); ok {
			out = m
		}
	}

	body, err := json.Marshal(out)
	if err != nil {
		log.Warn(ctx, "failed to marshal audit payload")
		return nil
	}
	return body
}

func getFieldForAction(ctx context.Context, action string) []string {
	auditRulesMutex.RLock()
	defer auditRulesMutex.RUnlock()

	if action != "" {
		if fields, ok := auditRules[action]; ok && len(fields) > 0 {
			return fields
		}
	}

	log.Warn(ctx, "audit rules are not defined for this action send default")
	return auditRules["default"]
}

func projectPath(cur interface{}, parts []string) (interface{}, bool) {
	if len(parts) == 0 {
		return cur, true
	}

	switch node := cur.(type) {
	case map[string]interface{}:
		next, ok := node[parts[0]]
		if !ok {
			return nil, false
		}
		child, ok := projectPath(next, parts[1:])
		if !ok {
			return nil, false
		}
		return map[string]interface{}{parts[0]: child}, true

	case []interface{}:
		out := make([]interface{}, 0, len(node))
		found := false

		for _, n := range node {
			child, ok := projectPath(n, parts)
			if ok {
				out = append(out, child)
				found = true
			}
		}
		if !found {
			return nil, false
		}
		return out, true

	default:
		return nil, false
	}
}
func deepMerge(dst, src interface{}) interface{} {
	if dst == nil {
		return src
	}

	dm, dok := dst.(map[string]interface{})
	sm, sok := src.(map[string]interface{})
	if dok && sok {
		for k, sv := range sm {
			if dv, ok := dm[k]; ok {
				dm[k] = deepMerge(dv, sv)
			} else {
				dm[k] = sv
			}
		}
		return dm
	}

	da, dok := dst.([]interface{})
	sa, sok := src.([]interface{})
	if dok && sok {
		if len(da) < len(sa) {
			ext := make([]interface{}, len(sa)-len(da))
			da = append(da, ext...)
		}

		for i := range sa {
			da[i] = deepMerge(da[i], sa[i])
		}
		return da
	}

	return src
}

func StartAuditFieldsRefresh(ctx context.Context, configUrl string, intervalSec int64) (stop func()) {

	if intervalSec <= 0 {
		intervalSec = 3600
	}

	interval := time.Duration(intervalSec) * time.Second
	ticker := time.NewTicker(interval)
	done := make(chan struct{})

	if err := loadAuditFieldRules(ctx, configUrl); err != nil {
		log.Warn(ctx, "failed to load audit rules from url")
	}

	go func() {
		for {
			select {
			case <-done:
				ticker.Stop()
				return
			case <-ticker.C:
				if err := loadAuditFieldRules(ctx, configUrl); err != nil {
					log.Warn(ctx, "failed to load audit rules from url")
				}
			}
		}
	}()

	return func() { close(done) }
}
