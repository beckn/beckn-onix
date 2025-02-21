package main

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestLoadConfig(t *testing.T) {
	tests := []struct {
		name        string
		wantAppName string
		wantPort    int
		err         bool
	}{
		{
			name:        "failed - Invalid config",
			wantAppName: "testNetworkSideHandler",
			wantPort:    7071,
			err:         true,
		},

		{
			name:        "Success - Valid config",
			wantAppName: "networkSideHandler",
			wantPort:    9091,
			err:         false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {

			config := loadConfig()

			if config.AppName == tt.wantAppName && tt.err == true {
				t.Errorf("%s: Expected appName: %s and port: %d, got appName: %s, port: %d", tt.name, tt.wantAppName, tt.wantPort, config.AppName, config.Port)
			}

			if config.AppName != tt.wantAppName && tt.err == false {
				t.Errorf("%s: Expected appName: %s and port: %d, got appName: %s, port: %d", tt.name, tt.wantAppName, tt.wantPort, config.AppName, config.Port)
			}
		})
	}
}

func TestCreatePostHandler(t *testing.T) {
	go func () {
		main()
	}()
	tests := []struct {
		name       string
		method     string
		expectCode int
	}{
		{"Valid POST Request", http.MethodPost, http.StatusOK},
		{"Invalid GET Request", http.MethodGet, http.StatusMethodNotAllowed},
		{"Invalid PUT Request", http.MethodPut, http.StatusMethodNotAllowed},
		{"Invalid DELETE Request", http.MethodDelete, http.StatusMethodNotAllowed},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest(tt.method, "/", nil)
			w := httptest.NewRecorder()
			CreatePostHandler(w, req)
			if w.Code != tt.expectCode {
				t.Errorf("%s: Expected status %d, got %d", tt.name, tt.expectCode, w.Code)
			}
		})
	}
}
