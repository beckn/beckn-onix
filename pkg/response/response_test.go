package response

import (
	"context"
	"encoding/json"
	"reflect"
	"testing"
)

func TestNack(t *testing.T) {
	ctx := context.Background()

	tests := []struct {
		name        string
		errorType   ErrorType
		requestBody string
		wantStatus  string
		wantErrCode string
		wantErrMsg  string
		wantErr     bool
		path        string
	}{
		{
			name:        "Schema validation error",
			errorType:   SchemaValidationErrorType,
			requestBody: `{"context": {"domain": "test-domain", "location": "test-location"}}`,
			wantStatus:  "NACK",
			wantErrCode: "400",
			wantErrMsg:  "Schema validation failed",
			wantErr:     false,
			path:        "test",
		},
		{
			name:        "Invalid request error",
			errorType:   InvalidRequestErrorType,
			requestBody: `{"context": {"domain": "test-domain"}}`,
			wantStatus:  "NACK",
			wantErrCode: "401",
			wantErrMsg:  "Invalid request format",
			wantErr:     false,
			path:        "test",
		},
		{
			name:        "Unknown error type",
			errorType:   "UNKNOWN_ERROR",
			requestBody: `{"context": {"domain": "test-domain"}}`,
			wantStatus:  "NACK",
			wantErrCode: "500",
			wantErrMsg:  "Internal server error",
			wantErr:     false,
			path:        "test",
		},
		{
			name:        "Empty request body",
			errorType:   SchemaValidationErrorType,
			requestBody: `{}`,
			wantStatus:  "NACK",
			wantErrCode: "400",
			wantErrMsg:  "Schema validation failed",
			wantErr:     false,
			path:        "test",
		},
		{
			name:        "Invalid JSON",
			errorType:   SchemaValidationErrorType,
			requestBody: `{invalid json}`,
			wantErr:     true,
			path:        "test",
		},
		{
			name:        "Complex nested context",
			errorType:   SchemaValidationErrorType,
			requestBody: `{"context": {"domain": "test-domain", "nested": {"key1": "value1", "key2": 123}}}`,
			wantStatus:  "NACK",
			wantErrCode: "400",
			wantErrMsg:  "Schema validation failed",
			wantErr:     false,
			path:        "test",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			resp, err := Nack(ctx, tt.errorType, tt.path, []byte(tt.requestBody))

			if (err != nil) != tt.wantErr {
				t.Errorf("Nack() error = %v, wantErr %v", err, tt.wantErr)
				return
			}

			if tt.wantErr && err != nil {
				return
			}

			var becknResp BecknResponse
			if err := json.Unmarshal(resp, &becknResp); err != nil {
				t.Errorf("Failed to unmarshal response: %v", err)
				return
			}

			if becknResp.Message.Ack.Status != tt.wantStatus {
				t.Errorf("Nack() status = %v, want %v", becknResp.Message.Ack.Status, tt.wantStatus)
			}

			if becknResp.Message.Error.Code != tt.wantErrCode {
				t.Errorf("Nack() error code = %v, want %v", becknResp.Message.Error.Code, tt.wantErrCode)
			}

			if becknResp.Message.Error.Message != tt.wantErrMsg {
				t.Errorf("Nack() error message = %v, want %v", becknResp.Message.Error.Message, tt.wantErrMsg)
			}

			var origReq BecknRequest
			if err := json.Unmarshal([]byte(tt.requestBody), &origReq); err == nil {
				if !compareContexts(becknResp.Context, origReq.Context) {
					t.Errorf("Nack() context not preserved, got = %v, want %v", becknResp.Context, origReq.Context)
				}
			}
		})
	}
}

func TestAck(t *testing.T) {
	ctx := context.Background()

	tests := []struct {
		name        string
		requestBody string
		wantStatus  string
		wantErr     bool
	}{
		{
			name:        "Valid request",
			requestBody: `{"context": {"domain": "test-domain", "location": "test-location"}}`,
			wantStatus:  "ACK",
			wantErr:     false,
		},
		{
			name:        "Empty context",
			requestBody: `{"context": {}}`,
			wantStatus:  "ACK",
			wantErr:     false,
		},
		{
			name:        "Invalid JSON",
			requestBody: `{invalid json}`,
			wantErr:     true,
		},
		{
			name:        "Complex nested context",
			requestBody: `{"context": {"domain": "test-domain", "nested": {"key1": "value1", "key2": 123, "array": [1,2,3]}}}`,
			wantStatus:  "ACK",
			wantErr:     false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			resp, err := Ack(ctx, []byte(tt.requestBody))

			if (err != nil) != tt.wantErr {
				t.Errorf("Ack() error = %v, wantErr %v", err, tt.wantErr)
				return
			}

			if tt.wantErr && err != nil {
				return
			}

			var becknResp BecknResponse
			if err := json.Unmarshal(resp, &becknResp); err != nil {
				t.Errorf("Failed to unmarshal response: %v", err)
				return
			}

			if becknResp.Message.Ack.Status != tt.wantStatus {
				t.Errorf("Ack() status = %v, want %v", becknResp.Message.Ack.Status, tt.wantStatus)
			}

			if becknResp.Message.Error != nil {
				t.Errorf("Ack() should not have error, got %v", becknResp.Message.Error)
			}

			var origReq BecknRequest
			if err := json.Unmarshal([]byte(tt.requestBody), &origReq); err == nil {
				if !compareContexts(becknResp.Context, origReq.Context) {
					t.Errorf("Ack() context not preserved, got = %v, want %v", becknResp.Context, origReq.Context)
				}
			}
		})
	}
}

func TestHandleClientFailure(t *testing.T) {
	ctx := context.Background()

	tests := []struct {
		name        string
		errorType   ErrorType
		requestBody string
		wantErrCode string
		wantErrMsg  string
		wantErr     bool
	}{
		{
			name:        "Schema validation error",
			errorType:   SchemaValidationErrorType,
			requestBody: `{"context": {"domain": "test-domain", "location": "test-location"}}`,
			wantErrCode: "400",
			wantErrMsg:  "Schema validation failed",
			wantErr:     false,
		},
		{
			name:        "Invalid request error",
			errorType:   InvalidRequestErrorType,
			requestBody: `{"context": {"domain": "test-domain"}}`,
			wantErrCode: "401",
			wantErrMsg:  "Invalid request format",
			wantErr:     false,
		},
		{
			name:        "Unknown error type",
			errorType:   "UNKNOWN_ERROR",
			requestBody: `{"context": {"domain": "test-domain"}}`,
			wantErrCode: "500",
			wantErrMsg:  "Internal server error",
			wantErr:     false,
		},
		{
			name:        "Invalid JSON",
			errorType:   SchemaValidationErrorType,
			requestBody: `{invalid json}`,
			wantErr:     true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			resp, err := HandleClientFailure(ctx, tt.errorType, []byte(tt.requestBody))

			if (err != nil) != tt.wantErr {
				t.Errorf("HandleClientFailure() error = %v, wantErr %v", err, tt.wantErr)
				return
			}

			if tt.wantErr && err != nil {
				return
			}

			var failureResp ClientFailureBecknResponse
			if err := json.Unmarshal(resp, &failureResp); err != nil {
				t.Errorf("Failed to unmarshal response: %v", err)
				return
			}

			if failureResp.Error.Code != tt.wantErrCode {
				t.Errorf("HandleClientFailure() error code = %v, want %v", failureResp.Error.Code, tt.wantErrCode)
			}

			if failureResp.Error.Message != tt.wantErrMsg {
				t.Errorf("HandleClientFailure() error message = %v, want %v", failureResp.Error.Message, tt.wantErrMsg)
			}

			var origReq BecknRequest
			if err := json.Unmarshal([]byte(tt.requestBody), &origReq); err == nil {
				if !compareContexts(failureResp.Context, origReq.Context) {
					t.Errorf("HandleClientFailure() context not preserved, got = %v, want %v", failureResp.Context, origReq.Context)
				}
			}
		})
	}
}

func TestErrorMap(t *testing.T) {

	expectedTypes := []ErrorType{
		SchemaValidationErrorType,
		InvalidRequestErrorType,
	}

	for _, tp := range expectedTypes {
		if _, exists := errorMap[tp]; !exists {
			t.Errorf("ErrorType %v not found in errorMap", tp)
		}
	}

	if DefaultError.Code != "500" || DefaultError.Message != "Internal server error" {
		t.Errorf("DefaultError not set correctly, got code=%v, message=%v", DefaultError.Code, DefaultError.Message)
	}
}

func compareContexts(c1, c2 map[string]interface{}) bool {

	if c1 == nil && c2 == nil {
		return true
	}

	if c1 == nil && len(c2) == 0 || c2 == nil && len(c1) == 0 {
		return true
	}

	return reflect.DeepEqual(c1, c2)
}

func TestSchemaValidationErr_Error(t *testing.T) {
	validationErrors := []Error{
		{Paths: "name", Message: "Name is required"},
		{Paths: "email", Message: "Invalid email format"},
	}
	err := SchemaValidationErr{Errors: validationErrors}
	expected := "name: Name is required; email: Invalid email format"
	if err.Error() != expected {
		t.Errorf("err.Error() = %s, want %s",
			err.Error(), expected)

	}
}
