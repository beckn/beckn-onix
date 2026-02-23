package telemetry

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"sync"

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

func LoadAuditFieldRules(ctx context.Context, configPath string) error {

	if strings.TrimSpace(configPath) == "" {
		err := fmt.Errorf("config file path is empty")
		log.Error(ctx, err, "there are no audit rules defined")
		return err
	}

	data, err := os.ReadFile(configPath)
	if err != nil {
		log.Error(ctx, err, "failed to read audit rules file")
		return err
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

//func getByPath(root map[string]interface{}, path string) (interface{}, bool) {
//
//	parts := strings.Split(path, ".")
//	var cur interface{} = root
//
//	for _, part := range parts {
//		m, ok := cur.(map[string]interface{})
//		if !ok {
//			return nil, false
//		}
//		v, ok := m[part]
//		if !ok {
//			return nil, false
//		}
//		cur = v
//	}
//	return cur, true
//}
//
//func setByPath(root map[string]interface{}, path string, value interface{}) {
//	parts := strings.Split(path, ".")
//	cur := root
//
//	for i := 0; i < len(parts)-1; i++ {
//		k := parts[i]
//		next, ok := cur[k].(map[string]interface{})
//		if !ok {
//			next = map[string]interface{}{}
//			cur[k] = next
//		}
//		cur = next
//	}
//	cur[parts[len(parts)-1]] = value
//}

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
