package handler

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestEndpointHandler_ServeHTTP(t *testing.T) {
	tests := []struct {
		name           string
		decode         func(ctx context.Context, r *http.Request) (string, error)
		execute        func(ctx context.Context, req string) (int, error)
		encode         func(w http.ResponseWriter, r *http.Request, req string, resp int, err error) (called bool, gotReq string, gotResp int, gotErr error)
		wantStatus     int
		wantBody       string
		wantEncodeCall bool
	}{
		{
			name: "decode error yields 400 with err.Error() body",
			decode: func(ctx context.Context, r *http.Request) (string, error) {
				return "", errors.New("bad request body")
			},
			execute: func(ctx context.Context, req string) (int, error) {
				t.Fatalf("execute should not be called when decode fails")
				return 0, nil
			},
			wantStatus: http.StatusBadRequest,
			wantBody:   "bad request body\n",
		},
		{
			name: "execute error still calls encode with zero resp and err",
			decode: func(ctx context.Context, r *http.Request) (string, error) {
				return "req1", nil
			},
			execute: func(ctx context.Context, req string) (int, error) {
				return 0, errors.New("execute failed")
			},
			wantEncodeCall: true,
			wantStatus:     http.StatusOK,
			wantBody:       "encoded:req1:0:execute failed",
		},
		{
			name: "success path calls encode with resp and nil error",
			decode: func(ctx context.Context, r *http.Request) (string, error) {
				return "req2", nil
			},
			execute: func(ctx context.Context, req string) (int, error) {
				return 42, nil
			},
			wantEncodeCall: true,
			wantStatus:     http.StatusOK,
			wantBody:       "encoded:req2:42:<nil>",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var encodeCalled bool
			ep := &EndpointHandler[string, int]{
				Decode:  tt.decode,
				Execute: tt.execute,
				Encode: func(w http.ResponseWriter, r *http.Request, req string, resp int, err error) {
					encodeCalled = true
					if err != nil {
						w.Write([]byte("encoded:" + req + ":" + itoa(resp) + ":" + err.Error()))
					} else {
						w.Write([]byte("encoded:" + req + ":" + itoa(resp) + ":<nil>"))
					}
				},
			}

			req := httptest.NewRequest(http.MethodPost, "/", nil)
			rec := httptest.NewRecorder()
			ep.ServeHTTP(rec, req)

			if encodeCalled != tt.wantEncodeCall {
				t.Fatalf("encode called = %v, want %v", encodeCalled, tt.wantEncodeCall)
			}
			if rec.Code != tt.wantStatus {
				t.Fatalf("status = %d, want %d", rec.Code, tt.wantStatus)
			}
			body, _ := io.ReadAll(rec.Body)
			if string(body) != tt.wantBody {
				t.Fatalf("body = %q, want %q", string(body), tt.wantBody)
			}
		})
	}
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var buf [20]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		i--
		buf[i] = '-'
	}
	return string(buf[i:])
}
