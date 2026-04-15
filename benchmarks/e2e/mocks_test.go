package e2e_bench_test

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"time"
)

// startMockBPP starts an httptest server that accepts any POST request and
// immediately returns a valid Beckn ACK. This replaces the real BPP backend,
// isolating benchmark results to adapter-internal latency only.
func startMockBPP() *httptest.Server {
	ackBody := `{"message":{"ack":{"status":"ACK"}}}`
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		fmt.Fprint(w, ackBody)
	}))
}

// subscriberRecord mirrors the registry API response shape for a single subscriber.
type subscriberRecord struct {
	SubscriberID    string `json:"subscriber_id"`
	UniqueKeyID     string `json:"unique_key_id"`
	SigningPublicKey string `json:"signing_public_key"`
	ValidFrom       string `json:"valid_from"`
	ValidUntil      string `json:"valid_until"`
	Status          string `json:"status"`
}

// startMockRegistry starts an httptest server that returns a subscriber record
// matching the benchmark test keys. The signvalidator plugin uses this to
// resolve the public key for signature verification on incoming requests.
func startMockRegistry() *httptest.Server {
	record := subscriberRecord{
		SubscriberID:    benchSubscriberID,
		UniqueKeyID:     benchKeyID,
		SigningPublicKey: benchPubKey,
		ValidFrom:       time.Now().AddDate(-1, 0, 0).Format(time.RFC3339),
		ValidUntil:      time.Now().AddDate(10, 0, 0).Format(time.RFC3339),
		Status:          "SUBSCRIBED",
	}
	body, _ := json.Marshal([]subscriberRecord{record})

	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Support both GET (lookup) and POST (lookup with body) registry calls.
		// Respond with the subscriber record regardless of subscriber_id query param.
		subscriberID := r.URL.Query().Get("subscriber_id")
		if subscriberID == "" {
			// Try extracting from path for dedi-registry style calls.
			parts := strings.Split(strings.TrimPrefix(r.URL.Path, "/"), "/")
			if len(parts) > 0 {
				subscriberID = parts[len(parts)-1]
			}
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		w.Write(body)
	}))
}
