package client

import (
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestError_Error(t *testing.T) {
	tests := []struct {
		name        string
		clientError Error
		expected    string
	}{
		{
			name: "Error with RawError",
			clientError: Error{
				Message:  "Failed to make request",
				RawError: errors.New("connection timeout"),
			},
			expected: "Failed to make request: connection timeout",
		},
		{
			name: "Error without RawError",
			clientError: Error{
				Message:  "Invalid response",
				RawError: nil,
			},
			expected: "Invalid response",
		},
		{
			name: "Error with empty message and RawError",
			clientError: Error{
				Message:  "",
				RawError: errors.New("internal error"),
			},
			expected: ": internal error",
		},
		{
			name: "Error with empty message and no RawError",
			clientError: Error{
				Message:  "",
				RawError: nil,
			},
			expected: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := tt.clientError.Error()
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestError_Unwrap(t *testing.T) {
	tests := []struct {
		name        string
		clientError Error
		expected    error
	}{
		{
			name: "Unwrap with RawError",
			clientError: Error{
				Message:  "API Error",
				RawError: errors.New("network error"),
			},
			expected: errors.New("network error"),
		},
		{
			name: "Unwrap without RawError",
			clientError: Error{
				Message:  "API Error",
				RawError: nil,
			},
			expected: nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := tt.clientError.Unwrap()
			if tt.expected == nil {
				assert.Nil(t, result)
			} else {
				assert.Equal(t, tt.expected.Error(), result.Error())
			}
		})
	}
}

func TestError_FullErrorObject(t *testing.T) {
	rawResponse := []byte(`{"error": "test error"}`)
	err := Error{
		Message:        "API request failed",
		StatusCode:     500,
		RawError:       errors.New("internal server error"),
		RawAPIResponse: rawResponse,
	}

	assert.Equal(t, "API request failed", err.Message)
	assert.Equal(t, 500, err.StatusCode)
	assert.Equal(t, "internal server error", err.RawError.Error())
	assert.Equal(t, rawResponse, err.RawAPIResponse)

	assert.Equal(t, "API request failed: internal server error", err.Error())

	assert.Equal(t, err.RawError, err.Unwrap())
}

func TestError_ErrorsIs(t *testing.T) {
	baseErr := errors.New("base error")
	clientErr := &Error{
		Message:  "Client error",
		RawError: baseErr,
	}

	assert.True(t, errors.Is(clientErr, baseErr))
	assert.False(t, errors.Is(clientErr, errors.New("different error")))
}

func TestError_ErrorsAs(t *testing.T) {
	clientErr := &Error{
		Message:    "Client error",
		StatusCode: 500,
		RawError:   errors.New("base error"),
	}

	var targetErr *Error
	assert.True(t, errors.As(clientErr, &targetErr))
	assert.Equal(t, clientErr, targetErr)
}
