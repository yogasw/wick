package client

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type TestResponse struct {
	Message string `json:"message"`
}

func TestNew(t *testing.T) {
	client := New()
	assert.NotNil(t, client)
	assert.NotNil(t, client.HTTPClient)
	assert.False(t, client.DebugMode)
}

func TestClient_Call(t *testing.T) {
	tests := []struct {
		name           string
		method         string
		responseStatus int
		responseBody   string
		headers        map[string]string
		requestBody    string
		expectedError  bool
		expectedResp   *TestResponse
	}{
		{
			name:           "successful GET request",
			method:         "GET",
			responseStatus: http.StatusOK,
			responseBody:   `{"message": "success"}`,
			expectedResp:   &TestResponse{Message: "success"},
		},
		{
			name:           "successful POST request with headers",
			method:         "POST",
			responseStatus: http.StatusOK,
			responseBody:   `{"message": "created"}`,
			headers:        map[string]string{"Authorization": "Bearer token"},
			requestBody:    `{"data": "test"}`,
			expectedResp:   &TestResponse{Message: "created"},
		},
		{
			name:           "error response",
			method:         "GET",
			responseStatus: http.StatusBadRequest,
			responseBody:   `{"error": "bad request"}`,
			expectedError:  true,
		},
		{
			name:           "invalid JSON response",
			method:         "GET",
			responseStatus: http.StatusOK,
			responseBody:   `invalid json`,
			expectedError:  true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Create test server
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				// Verify method
				assert.Equal(t, strings.ToUpper(tt.method), r.Method)

				// Verify headers
				assert.Equal(t, "application/json", r.Header.Get("Content-Type"))
				assert.Equal(t, "application/json", r.Header.Get("Accept"))
				for k, v := range tt.headers {
					assert.Equal(t, v, r.Header.Get(k))
				}

				// Verify request body if provided
				if tt.requestBody != "" {
					body, err := io.ReadAll(r.Body)
					require.NoError(t, err)
					assert.JSONEq(t, tt.requestBody, string(body))
				}

				// Send response
				w.WriteHeader(tt.responseStatus)
				w.Write([]byte(tt.responseBody))
			}))
			defer server.Close()

			// Create client
			client := New()
			client.DebugMode = true

			// Create request body if needed
			var body io.Reader
			if tt.requestBody != "" {
				body = strings.NewReader(tt.requestBody)
			}

			// Make request
			var response TestResponse
			err := client.Call(context.Background(), tt.method, server.URL, body, tt.headers, &response)

			if tt.expectedError {
				assert.Error(t, err)
				return
			}

			assert.NoError(t, err)
			assert.Equal(t, tt.expectedResp, &response)
		})
	}
}

func TestClient_Call_RequestError(t *testing.T) {
	client := New()
	err := client.Call(context.Background(), "GET", "invalid-url", nil, nil, nil)
	assert.Error(t, err)
}

func TestClient_Call_ContextCanceled(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Simulate a slow response
		time.Sleep(100 * time.Millisecond)
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	client := New()
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // Cancel immediately

	err := client.Call(ctx, "GET", server.URL, nil, nil, nil)
	assert.Error(t, err)
}

func TestError_Implementation(t *testing.T) {
	err := &Error{
		Message:        "test error",
		StatusCode:     400,
		RawError:       fmt.Errorf("raw error"),
		RawAPIResponse: []byte("api response"),
	}

	assert.Implements(t, (*error)(nil), err)
	assert.Contains(t, err.Error(), "test error")
}
