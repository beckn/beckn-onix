// Benchmarks for policy enforcer evaluation scaling.
// Measures how OPA evaluation time changes with rule count (1 to 500 rules),
// covering both realistic (mostly inactive) and worst-case (all active) scenarios.
// Also benchmarks compilation time (one-time startup cost).
//
// Run human-readable report:  go test -run TestBenchmarkReport -v -count=1
// Run Go benchmarks:          go test -bench=. -benchmem -count=1
package opapolicychecker

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// generateDummyRules creates a .rego policy file with N rules.
// Only one rule matches the input (action == "confirm"), the rest have impossible
// conditions (action == "foobar1", "foobar2", ...) to simulate realistic rule bloat
// where most rules don't fire.
func generateDummyRules(n int) string {
	var sb strings.Builder
	sb.WriteString("package policy\nimport rego.v1\n\n")

	// One real rule that actually fires
	sb.WriteString("violations contains \"real_violation\" if {\n")
	sb.WriteString("    input.context.action == \"confirm\"\n")
	sb.WriteString("    input.message.order.value > 10000\n")
	sb.WriteString("}\n\n")

	// N-1 dummy rules with impossible conditions (simulate rule bloat)
	for i := 1; i < n; i++ {
		sb.WriteString(fmt.Sprintf("violations contains \"dummy_violation_%d\" if {\n", i))
		sb.WriteString(fmt.Sprintf("    input.context.action == \"foobar%d\"\n", i))
		sb.WriteString(fmt.Sprintf("    input.message.order.value > %d\n", i*100))
		sb.WriteString("}\n\n")
	}

	return sb.String()
}

// generateActiveRules creates N rules that ALL fire on the test input.
// This is the worst case: every rule matches.
func generateActiveRules(n int) string {
	var sb strings.Builder
	sb.WriteString("package policy\nimport rego.v1\n\n")

	for i := 0; i < n; i++ {
		sb.WriteString(fmt.Sprintf("violations contains \"active_violation_%d\" if {\n", i))
		sb.WriteString("    input.context.action == \"confirm\"\n")
		sb.WriteString("}\n\n")
	}

	return sb.String()
}

// sampleBecknInput is a realistic beckn confirm message for benchmarking.
var sampleBecknInput = []byte(`{
	"context": {
		"domain": "energy",
		"action": "confirm",
		"version": "1.1.0",
		"bap_id": "buyer-bap.example.com",
		"bap_uri": "https://buyer-bap.example.com",
		"bpp_id": "seller-bpp.example.com",
		"bpp_uri": "https://seller-bpp.example.com",
		"transaction_id": "txn-12345",
		"message_id": "msg-67890",
		"timestamp": "2026-03-04T10:00:00Z"
	},
	"message": {
		"order": {
			"id": "order-001",
			"provider": {"id": "seller-1"},
			"items": [
				{"id": "item-1", "quantity": {"selected": {"count": 100}}},
				{"id": "item-2", "quantity": {"selected": {"count": 50}}}
			],
			"value": 15000,
			"fulfillment": {
				"type": "DELIVERY",
				"start": {"time": {"timestamp": "2026-03-05T08:00:00Z"}},
				"end": {"time": {"timestamp": "2026-03-05T18:00:00Z"}}
			}
		}
	}
}`)

// --- Go Benchmarks (run with: go test -bench=. -benchmem) ---

// BenchmarkEvaluate_MostlyInactive benchmarks evaluation with N rules where
// only 1 rule fires. This simulates a realistic governance ruleset where
// most rules are for different actions/conditions.
func BenchmarkEvaluate_MostlyInactive(b *testing.B) {
	sizes := []int{1, 10, 50, 100, 250, 500}
	for _, n := range sizes {
		b.Run(fmt.Sprintf("rules=%d", n), func(b *testing.B) {
			dir := b.TempDir()
			os.WriteFile(filepath.Join(dir, "policy.rego"), []byte(generateDummyRules(n)), 0644)

			eval, err := NewEvaluator([]string{dir}, "data.policy.violations", nil, false, 0, nil)
			if err != nil {
				b.Fatalf("NewEvaluator failed: %v", err)
			}

			ctx := context.Background()

			violations, err := eval.Evaluate(ctx, sampleBecknInput)
			if err != nil {
				b.Fatalf("correctness check failed: %v", err)
			}
			if len(violations) != 1 || violations[0] != "real_violation" {
				b.Fatalf("expected [real_violation], got %v", violations)
			}

			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				_, err := eval.Evaluate(ctx, sampleBecknInput)
				if err != nil {
					b.Fatalf("Evaluate failed: %v", err)
				}
			}
		})
	}
}

// BenchmarkEvaluate_AllActive benchmarks the worst case where ALL N rules fire.
func BenchmarkEvaluate_AllActive(b *testing.B) {
	sizes := []int{1, 10, 50, 100, 250, 500}
	for _, n := range sizes {
		b.Run(fmt.Sprintf("rules=%d", n), func(b *testing.B) {
			dir := b.TempDir()
			os.WriteFile(filepath.Join(dir, "policy.rego"), []byte(generateActiveRules(n)), 0644)

			eval, err := NewEvaluator([]string{dir}, "data.policy.violations", nil, false, 0, nil)
			if err != nil {
				b.Fatalf("NewEvaluator failed: %v", err)
			}

			ctx := context.Background()

			violations, err := eval.Evaluate(ctx, sampleBecknInput)
			if err != nil {
				b.Fatalf("correctness check failed: %v", err)
			}
			if len(violations) != n {
				b.Fatalf("expected %d violations, got %d", n, len(violations))
			}

			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				_, err := eval.Evaluate(ctx, sampleBecknInput)
				if err != nil {
					b.Fatalf("Evaluate failed: %v", err)
				}
			}
		})
	}
}

// BenchmarkCompilation measures how long it takes to compile policies of various sizes.
// This runs once at startup, so it's less critical but good to know.
func BenchmarkCompilation(b *testing.B) {
	sizes := []int{10, 50, 100, 250, 500}
	for _, n := range sizes {
		b.Run(fmt.Sprintf("rules=%d", n), func(b *testing.B) {
			dir := b.TempDir()
			os.WriteFile(filepath.Join(dir, "policy.rego"), []byte(generateDummyRules(n)), 0644)

			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				_, err := NewEvaluator([]string{dir}, "data.policy.violations", nil, false, 0, nil)
				if err != nil {
					b.Fatalf("NewEvaluator failed: %v", err)
				}
			}
		})
	}
}

// --- Human-Readable Report (run with: go test -run TestBenchmarkReport -v) ---

// TestBenchmarkReport generates a readable table showing how evaluation time
// scales with rule count. This is the report to share with the team.
func TestBenchmarkReport(t *testing.T) {
	sizes := []int{1, 10, 50, 100, 250, 500}
	iterations := 1000

	fmt.Println()
	fmt.Println("╔══════════════════════════════════════════════════════════════════════╗")
	fmt.Println("║        Policy Enforcer — Performance Benchmark Report               ║")
	fmt.Println("╠══════════════════════════════════════════════════════════════════════╣")
	fmt.Println()

	// --- Compilation time ---
	fmt.Println("┌─────────────────────────────────────────────────┐")
	fmt.Println("│ Compilation Time (one-time startup cost)        │")
	fmt.Println("├──────────┬──────────────────────────────────────┤")
	fmt.Println("│ Rules    │ Compilation Time                     │")
	fmt.Println("├──────────┼──────────────────────────────────────┤")
	for _, n := range sizes {
		dir := t.TempDir()
		os.WriteFile(filepath.Join(dir, "policy.rego"), []byte(generateDummyRules(n)), 0644)

		start := time.Now()
		_, err := NewEvaluator([]string{dir}, "data.policy.violations", nil, false, 0, nil)
		elapsed := time.Since(start)
		if err != nil {
			t.Fatalf("NewEvaluator(%d rules) failed: %v", n, err)
		}
		fmt.Printf("│ %-8d │ %-36s │\n", n, elapsed.Round(time.Microsecond))
	}
	fmt.Println("└──────────┴──────────────────────────────────────┘")
	fmt.Println()

	// --- Evaluation time (mostly inactive rules) ---
	fmt.Println("┌─────────────────────────────────────────────────────────────────┐")
	fmt.Printf("│ Evaluation Time — Mostly Inactive Rules (%d iterations)       │\n", iterations)
	fmt.Println("│ (1 rule fires, rest have non-matching conditions)               │")
	fmt.Println("├──────────┬──────────────┬──────────────┬────────────────────────┤")
	fmt.Println("│ Rules    │ Avg/eval     │ p99          │ Violations             │")
	fmt.Println("├──────────┼──────────────┼──────────────┼────────────────────────┤")
	for _, n := range sizes {
		dir := t.TempDir()
		os.WriteFile(filepath.Join(dir, "policy.rego"), []byte(generateDummyRules(n)), 0644)

		eval, err := NewEvaluator([]string{dir}, "data.policy.violations", nil, false, 0, nil)
		if err != nil {
			t.Fatalf("NewEvaluator(%d rules) failed: %v", n, err)
		}

		ctx := context.Background()
		durations := make([]time.Duration, iterations)
		var lastViolations []string

		for i := 0; i < iterations; i++ {
			start := time.Now()
			v, err := eval.Evaluate(ctx, sampleBecknInput)
			durations[i] = time.Since(start)
			if err != nil {
				t.Fatalf("Evaluate failed: %v", err)
			}
			lastViolations = v
		}

		avg, p99 := calcStats(durations)
		fmt.Printf("│ %-8d │ %-12s │ %-12s │ %-22d │\n", n, avg.Round(time.Microsecond), p99.Round(time.Microsecond), len(lastViolations))
	}
	fmt.Println("└──────────┴──────────────┴──────────────┴────────────────────────┘")
	fmt.Println()

	// --- Evaluation time (all rules active) ---
	fmt.Println("┌─────────────────────────────────────────────────────────────────┐")
	fmt.Printf("│ Evaluation Time — All Rules Active (%d iterations)             │\n", iterations)
	fmt.Println("│ (every rule fires — worst case scenario)                        │")
	fmt.Println("├──────────┬──────────────┬──────────────┬────────────────────────┤")
	fmt.Println("│ Rules    │ Avg/eval     │ p99          │ Violations             │")
	fmt.Println("├──────────┼──────────────┼──────────────┼────────────────────────┤")
	for _, n := range sizes {
		dir := t.TempDir()
		os.WriteFile(filepath.Join(dir, "policy.rego"), []byte(generateActiveRules(n)), 0644)

		eval, err := NewEvaluator([]string{dir}, "data.policy.violations", nil, false, 0, nil)
		if err != nil {
			t.Fatalf("NewEvaluator(%d rules) failed: %v", n, err)
		}

		ctx := context.Background()
		durations := make([]time.Duration, iterations)
		var lastViolations []string

		for i := 0; i < iterations; i++ {
			start := time.Now()
			v, err := eval.Evaluate(ctx, sampleBecknInput)
			durations[i] = time.Since(start)
			if err != nil {
				t.Fatalf("Evaluate failed: %v", err)
			}
			lastViolations = v
		}

		avg, p99 := calcStats(durations)
		fmt.Printf("│ %-8d │ %-12s │ %-12s │ %-22d │\n", n, avg.Round(time.Microsecond), p99.Round(time.Microsecond), len(lastViolations))
	}
	fmt.Println("└──────────┴──────────────┴──────────────┴────────────────────────┘")
	fmt.Println()
	fmt.Println("╚══════════════════════════════════════════════════════════════════════╝")
}

// calcStats returns average and p99 durations from a sorted slice.
func calcStats(durations []time.Duration) (avg, p99 time.Duration) {
	n := len(durations)
	if n == 0 {
		return 0, 0
	}

	var total time.Duration
	for _, d := range durations {
		total += d
	}
	avg = total / time.Duration(n)

	// Sort for p99
	sorted := make([]time.Duration, n)
	copy(sorted, durations)
	sortDurations(sorted)
	p99 = sorted[int(float64(n)*0.99)]

	return avg, p99
}

// sortDurations sorts a slice of durations in ascending order (insertion sort, fine for 1000 items).
func sortDurations(d []time.Duration) {
	for i := 1; i < len(d); i++ {
		key := d[i]
		j := i - 1
		for j >= 0 && d[j] > key {
			d[j+1] = d[j]
			j--
		}
		d[j+1] = key
	}
}
