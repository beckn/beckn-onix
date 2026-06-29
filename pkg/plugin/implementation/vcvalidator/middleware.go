package vcvalidator

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
)

// NewMiddleware returns an HTTP middleware that validates Verifiable
// Credentials embedded in the request body. For the configured beckn actions
// it verifies every embedded credential's proof, validity window and
// revocation status. If any credential fails, the request is rejected with a
// beckn NACK and is NOT forwarded to the next handler.
func NewMiddleware(cfg map[string]string) (func(http.Handler) http.Handler, error) {
	config, err := ParseConfig(cfg)
	if err != nil {
		return nil, fmt.Errorf("vcvalidator: config: %w", err)
	}

	client := &http.Client{Timeout: config.HTTPTimeout}
	v := newVerifier(config, httpFetcher(client))
	v.statusGet = httpStatusFetcher(client)

	fmt.Printf("[VCValidator] enabled=%v actions=%v methods=%v checkExpiry=%v checkRevocation=%v requireProof=%v failOpen=%v\n",
		config.Enabled, config.Actions, config.AllowedDIDMethods,
		config.CheckExpiry, config.CheckRevocation, config.RequireProof, config.FailOpen)

	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if !config.Enabled {
				next.ServeHTTP(w, r)
				return
			}

			body, err := io.ReadAll(r.Body)
			if err != nil {
				// cannot read body — let the next handler deal with it.
				next.ServeHTTP(w, r)
				return
			}
			restore := func() {
				r.Body = io.NopCloser(bytes.NewReader(body))
				r.ContentLength = int64(len(body))
			}

			action := extractAction(r.URL.Path, body)
			if !config.IsActionEnabled(action) {
				restore()
				next.ServeHTTP(w, r)
				return
			}

			creds := extractCredentials(body)
			if len(creds) == 0 {
				if config.DebugLogging {
					fmt.Printf("[VCValidator] action=%s: no embedded credentials, passing through\n", action)
				}
				restore()
				next.ServeHTTP(w, r)
				return
			}

			for i, raw := range creds {
				if err := v.verify(r.Context(), raw); err != nil {
					ve := asVCError(err)
					fmt.Printf("[VCValidator] action=%s credential[%d] REJECTED: %s\n", action, i, ve.Error())
					writeNack(w, body, ve)
					return
				}
			}

			fmt.Printf("[VCValidator] action=%s: %d credential(s) verified OK\n", action, len(creds))
			restore()
			next.ServeHTTP(w, r)
		})
	}, nil
}

func asVCError(err error) *vcError {
	if ve, ok := err.(*vcError); ok {
		return ve
	}
	return &vcError{class: failStructure, msg: err.Error()}
}

// httpStatusFor maps a failure class to an HTTP status code.
func httpStatusFor(class failClass) int {
	switch class {
	case failStructure:
		return http.StatusBadRequest
	case failRevoked:
		return http.StatusForbidden
	default: // failProof, failIssuer, failExpired, failResolution
		return http.StatusUnauthorized
	}
}

// nackResponse mirrors beckn-onix's v2 NACK wire format
// ({"message":{"status":"NACK","messageId":...,"error":{...}}}).
type nackResponse struct {
	Message struct {
		Status    string `json:"status"`
		MessageID string `json:"messageId,omitempty"`
		Error     struct {
			Code    string `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	} `json:"message"`
}

func writeNack(w http.ResponseWriter, reqBody []byte, ve *vcError) {
	var resp nackResponse
	resp.Message.Status = "NACK"
	resp.Message.MessageID = messageID(reqBody)
	resp.Message.Error.Code = string(ve.class)
	resp.Message.Error.Message = ve.msg

	data, _ := json.Marshal(&resp)
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(httpStatusFor(ve.class))
	_, _ = w.Write(data)
}

func messageID(body []byte) string {
	var env struct {
		Context struct {
			MessageID string `json:"messageId"`
		} `json:"context"`
	}
	if err := json.Unmarshal(body, &env); err == nil {
		return env.Context.MessageID
	}
	return ""
}
