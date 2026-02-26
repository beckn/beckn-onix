package policyenforcer

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/beckn-one/beckn-onix/pkg/log"
	"github.com/beckn-one/beckn-onix/pkg/model"
)

// PolicyEnforcer is a Step plugin that evaluates beckn messages against
// OPA policies and NACKs non-compliant messages.
type PolicyEnforcer struct {
	config    *Config
	evaluator *Evaluator
}

// New creates a new PolicyEnforcer instance.
func New(cfg map[string]string) (*PolicyEnforcer, error) {
	config, err := ParseConfig(cfg)
	if err != nil {
		return nil, fmt.Errorf("policyenforcer: config error: %w", err)
	}

	evaluator, err := NewEvaluator(config.PolicyDir, config.PolicyFile, config.PolicyUrls, config.Query, config.RuntimeConfig)
	if err != nil {
		return nil, fmt.Errorf("policyenforcer: failed to initialize OPA evaluator: %w", err)
	}

	log.Infof(context.TODO(), "PolicyEnforcer initialized (actions=%v, query=%s, policies=%v, debugLogging=%v)",
		config.Actions, config.Query, evaluator.ModuleNames(), config.DebugLogging)

	return &PolicyEnforcer{
		config:    config,
		evaluator: evaluator,
	}, nil
}

// Run implements the Step interface. It evaluates the message body against
// loaded OPA policies. Returns a BadReqErr (causing NACK) if violations are found.
// Returns an error on evaluation failure (fail closed).
func (e *PolicyEnforcer) Run(ctx *model.StepContext) error {
	if !e.config.Enabled {
		log.Debug(ctx, "PolicyEnforcer: plugin disabled, skipping")
		return nil
	}

	// Extract action from the message
	action := extractAction(ctx.Request.URL.Path, ctx.Body)

	if !e.config.IsActionEnabled(action) {
		if e.config.DebugLogging {
			log.Debugf(ctx, "PolicyEnforcer: action %q not in configured actions %v, skipping", action, e.config.Actions)
		}
		return nil
	}

	if e.config.DebugLogging {
		log.Debugf(ctx, "PolicyEnforcer: evaluating policies for action %q (modules=%v)", action, e.evaluator.ModuleNames())
	}

	violations, err := e.evaluator.Evaluate(ctx, ctx.Body)
	if err != nil {
		// Fail closed: evaluation error → NACK
		log.Errorf(ctx, err, "PolicyEnforcer: policy evaluation failed: %v", err)
		return model.NewBadReqErr(fmt.Errorf("policy evaluation error: %w", err))
	}

	if len(violations) == 0 {
		if e.config.DebugLogging {
			log.Debugf(ctx, "PolicyEnforcer: message compliant for action %q", action)
		}
		return nil
	}

	// Non-compliant: NACK with all violation messages
	msg := fmt.Sprintf("policy violation(s): %s", strings.Join(violations, "; "))
	log.Warnf(ctx, "PolicyEnforcer: %s", msg)
	return model.NewBadReqErr(fmt.Errorf("%s", msg))
}

// Close is a no-op for the policy enforcer (no resources to release).
func (e *PolicyEnforcer) Close() {}

// extractAction gets the beckn action from the URL path or message body.
func extractAction(urlPath string, body []byte) string {
	// Try URL path first: /bap/receiver/{action} or /bpp/caller/{action}
	parts := strings.Split(strings.Trim(urlPath, "/"), "/")
	if len(parts) >= 3 {
		return parts[len(parts)-1]
	}

	// Fallback: extract from body context.action
	var payload struct {
		Context struct {
			Action string `json:"action"`
		} `json:"context"`
	}
	if err := json.Unmarshal(body, &payload); err == nil && payload.Context.Action != "" {
		return payload.Context.Action
	}

	return ""
}
