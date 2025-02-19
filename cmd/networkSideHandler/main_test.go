package main

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestCreateUserHandler(t *testing.T) {
	tests := []struct {
		name           string
		payload        string
		expectedStatus int
	}{
		{"Valid request", `{"name": "John Doe", "email": "john@example.com"}`, http.StatusCreated},
		{"Missing name", `{"email": "john@example.com"}`, http.StatusBadRequest},
		{"Missing email", `{"name": "John Doe"}`, http.StatusBadRequest},
		{"Invalid JSON", `{name: "John Doe", email: "john@example.com"}`, http.StatusBadRequest},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodPost, "/users", bytes.NewBuffer([]byte(tc.payload)))
			req.Header.Set("Content-Type", "application/json")

			rr := httptest.NewRecorder()
			CreateUserHandler(rr, req)

			require.Equal(t, tc.expectedStatus, rr.Code, "Unexpected response status")
		})

		// should fail when HTTP Method != POST
		req := httptest.NewRequest(http.MethodGet, "/users", bytes.NewBuffer([]byte(tc.payload)))
		req.Header.Set("Content-Type", "application/json")
		rr := httptest.NewRecorder()
		CreateUserHandler(rr, req)

		require.Equal(t, http.StatusMethodNotAllowed, rr.Code, "Unexpected response status")

	}
}
