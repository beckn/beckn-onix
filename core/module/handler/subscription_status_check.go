package handler

import (
	"context"
	"encoding/json"
	"fmt"
	"io/ioutil"
	"net/http"
	"time"

	"github.com/beckn/beckn-onix/core/module/client"
	"github.com/beckn/beckn-onix/pkg/log"
	"github.com/beckn/beckn-onix/pkg/model"
)

// SubscriptionStatusChecker manages asynchronous status lookups with retries.
type SubscriptionStatusChecker struct {
	Client          *client.Config
	MaxRetries      int
	RetryDelay      time.Duration
	registeryClient client.RegistryClientInterface
}

// Handler returns an HTTP handler that manages subscription status lookup.
func Handler(ctx context.Context, checker *SubscriptionStatusChecker) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var lookupReq model.LookupRequest
		if err := json.NewDecoder(r.Body).Decode(&lookupReq); err != nil {
			http.Error(w, "Invalid request payload", http.StatusBadRequest)
			return
		}

		subscribed, err := checker.LookupStatus(ctx, lookupReq)
		if err != nil {
			http.Error(w, fmt.Sprintf("Subscription lookup failed: %v", err), http.StatusInternalServerError)
			return
		}
		if subscribed {
			w.WriteHeader(http.StatusOK)
			fmt.Fprintln(w, "Subscription successful")
		} else {
			http.Error(w, "Subscription pending", http.StatusAccepted)
		}
	})
}

// NewSubscriptionStatusChecker initializes a new status checker.
func NewSubscriptionStatusChecker(clientConfig *client.Config, retries int, delay time.Duration) *SubscriptionStatusChecker {
	return &SubscriptionStatusChecker{
		Client:     clientConfig,
		MaxRetries: retries,
		RetryDelay: delay,
	}
}

// LookupStatus attempts multiple registry lookups until it finds a "SUBSCRIBED" response.
func (s *SubscriptionStatusChecker) LookupStatus(ctx context.Context, lookupReq model.LookupRequest) (bool, error) {
	for attempt := 1; attempt <= s.MaxRetries; attempt++ {
		status, err := s.fetchSubscriptionStatus(ctx, lookupReq)
		if err != nil {
			log.Errorf(ctx, err, "Attempt %d: Failed to fetch subscription status", attempt)
		} else if status == "SUBSCRIBED" {
			log.Infof(ctx, "Subscription confirmed for %s", lookupReq.SubscriberID)
			return true, nil
		}
		time.Sleep(s.RetryDelay)
	}
	return false, fmt.Errorf("subscription check timed out for subscriber %s", lookupReq.SubscriberID)
}

// fetchSubscriptionStatus performs the actual HTTP request for lookup.
func (s *SubscriptionStatusChecker) fetchSubscriptionStatus(ctx context.Context, lookupReq model.LookupRequest) (string, error) {
	reqBody, err := json.Marshal(lookupReq)
	if err != nil {
		return "", fmt.Errorf("failed to marshal lookup request: %w", err)
	}

	resp, err := s.registeryClient.CreateRequest(ctx, http.MethodPost, "lookup", reqBody)
	if err != nil {
		return "", fmt.Errorf("lookup request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("unexpected response code: %d", resp.StatusCode)
	}

	body, err := ioutil.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("failed to read response body: %w", err)
	}

	var lookupResp model.LookupResponse
	if err := json.Unmarshal(body, &lookupResp); err != nil {
		return "", fmt.Errorf("failed to parse lookup response: %w", err)
	}

	return lookupResp.Status, nil
}
