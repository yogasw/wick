package sanitizer

import (
	"encoding/json"
	"net/http"
	"net/url"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestSanitizeJSON_FlatObject(t *testing.T) {
	s := New()

	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{
			name:     "password field",
			input:    `{"username":"john","password":"secret123"}`,
			expected: `{"password":"******","username":"john"}`,
		},
		{
			name:     "api_key field",
			input:    `{"api_key":"abc123","data":"value"}`,
			expected: `{"api_key":"******","data":"value"}`,
		},
		{
			name:     "token field",
			input:    `{"user":"john","token":"xyz789"}`,
			expected: `{"token":"******","user":"john"}`,
		},
		{
			name:     "secret field",
			input:    `{"id":1,"secret":"topsecret"}`,
			expected: `{"id":1,"secret":"******"}`,
		},
		{
			name:     "multiple sensitive fields",
			input:    `{"username":"john","password":"pass123","api_key":"key456"}`,
			expected: `{"api_key":"******","password":"******","username":"john"}`,
		},
		{
			name:     "no sensitive fields",
			input:    `{"username":"john","email":"john@example.com"}`,
			expected: `{"email":"john@example.com","username":"john"}`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := s.SanitizeJSON([]byte(tt.input))
			assert.JSONEq(t, tt.expected, result)
		})
	}
}

func TestSanitizeJSON_NestedObject(t *testing.T) {
	s := New()

	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{
			name:     "nested secret",
			input:    `{"user":{"name":"john","secret_key":"xyz"}}`,
			expected: `{"user":{"name":"john","secret_key":"******"}}`,
		},
		{
			name:     "deeply nested",
			input:    `{"level1":{"level2":{"password":"secret"}}}`,
			expected: `{"level1":{"level2":{"password":"******"}}}`,
		},
		{
			name:     "nested with multiple fields",
			input:    `{"user":{"name":"john","auth_data":{"password":"pass","token":"tok"}}}`,
			expected: `{"user":{"auth_data":{"password":"******","token":"******"},"name":"john"}}`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := s.SanitizeJSON([]byte(tt.input))
			assert.JSONEq(t, tt.expected, result)
		})
	}
}

func TestSanitizeJSON_Arrays(t *testing.T) {
	s := New()

	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{
			name:     "array of objects with sensitive fields",
			input:    `[{"id":1,"token":"abc"},{"id":2,"token":"def"}]`,
			expected: `[{"id":1,"token":"******"},{"id":2,"token":"******"}]`,
		},
		{
			name:     "nested array",
			input:    `{"users":[{"name":"john","password":"pass1"},{"name":"jane","password":"pass2"}]}`,
			expected: `{"users":[{"name":"john","password":"******"},{"name":"jane","password":"******"}]}`,
		},
		{
			name:     "array of primitives",
			input:    `{"tags":["tag1","tag2","tag3"]}`,
			expected: `{"tags":["tag1","tag2","tag3"]}`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := s.SanitizeJSON([]byte(tt.input))
			assert.JSONEq(t, tt.expected, result)
		})
	}
}

func TestSanitizeJSON_CaseInsensitive(t *testing.T) {
	s := New()

	tests := []struct {
		name     string
		input    string
		checkFor string
	}{
		{
			name:     "uppercase PASSWORD",
			input:    `{"PASSWORD":"secret"}`,
			checkFor: "******",
		},
		{
			name:     "mixed case Password",
			input:    `{"Password":"secret"}`,
			checkFor: "******",
		},
		{
			name:     "camelCase apiKey",
			input:    `{"apiKey":"secret"}`,
			checkFor: "******",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := s.SanitizeJSON([]byte(tt.input))
			assert.Contains(t, result, tt.checkFor)
		})
	}
}

func TestSanitizeJSON_ExactMatching(t *testing.T) {
	s := New()

	tests := []struct {
		name     string
		input    string
		checkFor string
	}{
		{
			name:     "user_password exact match",
			input:    `{"user_password":"secret"}`,
			checkFor: "******",
		},
		{
			name:     "passwordhash exact match",
			input:    `{"passwordhash":"hash123"}`,
			checkFor: "******",
		},
		{
			name:     "bearer_token exact match",
			input:    `{"bearer_token":"token123"}`,
			checkFor: "******",
		},
		{
			name:     "api_key exact match",
			input:    `{"api_key":"key123"}`,
			checkFor: "******",
		},
		{
			name:     "client_secret exact match",
			input:    `{"client_secret":"secret123"}`,
			checkFor: "******",
		},
		{
			name:     "jwt exact match",
			input:    `{"jwt":"token123"}`,
			checkFor: "******",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := s.SanitizeJSON([]byte(tt.input))
			assert.Contains(t, result, tt.checkFor)
		})
	}
}

func TestSanitizeJSON_EdgeCases(t *testing.T) {
	s := New()

	tests := []struct {
		name     string
		input    []byte
		expected string
	}{
		{
			name:     "empty data",
			input:    []byte{},
			expected: "",
		},
		{
			name:     "invalid json",
			input:    []byte(`not a json`),
			expected: `not a json`,
		},
		{
			name:     "null value",
			input:    []byte(`{"password":null}`),
			expected: `{"password":"******"}`,
		},
		{
			name:     "number value",
			input:    []byte(`{"id":123,"username":"john"}`),
			expected: `{"id":123,"username":"john"}`,
		},
		{
			name:     "boolean value",
			input:    []byte(`{"active":true,"username":"john"}`),
			expected: `{"active":true,"username":"john"}`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := s.SanitizeJSON(tt.input)
			if tt.name == "null value" || tt.name == "number value" || tt.name == "boolean value" {
				assert.JSONEq(t, tt.expected, result)
			} else {
				assert.Equal(t, tt.expected, result)
			}
		})
	}
}

func TestSanitizeJSON_MixedDataTypes(t *testing.T) {
	s := New()

	input := `{
		"id": 123,
		"username": "john",
		"password": "secret",
		"active": true,
		"metadata": null,
		"roles": ["admin", "user"],
		"settings": {
			"theme": "dark",
			"api_key": "key123"
		}
	}`

	result := s.SanitizeJSON([]byte(input))

	var resultMap map[string]any
	err := json.Unmarshal([]byte(result), &resultMap)
	assert.NoError(t, err)

	assert.Equal(t, float64(123), resultMap["id"])
	assert.Equal(t, "john", resultMap["username"])
	assert.Equal(t, RedactedValue, resultMap["password"])
	assert.Equal(t, true, resultMap["active"])
	assert.Nil(t, resultMap["metadata"])

	settings := resultMap["settings"].(map[string]any)
	assert.Equal(t, "dark", settings["theme"])
	assert.Equal(t, RedactedValue, settings["api_key"])
}

func TestSanitizeHeaders(t *testing.T) {
	s := New()

	tests := []struct {
		name     string
		headers  http.Header
		expected map[string]string
	}{
		{
			name: "authorization header",
			headers: http.Header{
				"Authorization": []string{"Bearer token123"},
				"Content-Type":  []string{"application/json"},
			},
			expected: map[string]string{
				"Authorization": RedactedValue,
				"Content-Type":  "application/json",
			},
		},
		{
			name: "suffix match catches any -Secret-Key header",
			headers: http.Header{
				"Abc-Secret-Key": []string{"secret123"},
				"X-App-Secret":   []string{"appsecret456"},
				"Content-Type":   []string{"application/json"},
			},
			expected: map[string]string{
				"Abc-Secret-Key": RedactedValue,
				"X-App-Secret":   RedactedValue,
				"Content-Type":   "application/json",
			},
		},
		{
			name: "case insensitive matching",
			headers: http.Header{
				"Authorization": []string{"Bearer token123"},
				"Content-Type":  []string{"application/json"},
			},
			expected: map[string]string{
				"Authorization": RedactedValue,
				"Content-Type":  "application/json",
			},
		},
		{
			name: "multiple sensitive headers",
			headers: http.Header{
				"Authorization": []string{"Bearer token123"},
				"Cookie":        []string{"session=abc"},
				"X-Api-Key":     []string{"key123"},
				"Accept":        []string{"application/json"},
			},
			expected: map[string]string{
				"Authorization": RedactedValue,
				"Cookie":        RedactedValue,
				"X-Api-Key":     RedactedValue,
				"Accept":        "application/json",
			},
		},
		{
			name:     "nil headers",
			headers:  nil,
			expected: nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := s.SanitizeHeaders(tt.headers)

			if tt.expected == nil {
				assert.Nil(t, result)
				return
			}

			for key, expectedValue := range tt.expected {
				assert.Equal(t, expectedValue, result.Get(key))
			}
		})
	}
}

func TestSanitizeHeadersMap(t *testing.T) {
	s := New()

	tests := []struct {
		name     string
		headers  map[string]string
		expected map[string]string
	}{
		{
			name: "authorization header",
			headers: map[string]string{
				"Authorization": "Bearer token123",
				"Content-Type":  "application/json",
			},
			expected: map[string]string{
				"Authorization": RedactedValue,
				"Content-Type":  "application/json",
			},
		},
		{
			name: "suffix match catches any -Secret-Key header (map)",
			headers: map[string]string{
				"Abc-Secret-Key": "secret123",
				"X-App-Secret":   "appsecret456",
				"Content-Type":   "application/json",
			},
			expected: map[string]string{
				"Abc-Secret-Key": RedactedValue,
				"X-App-Secret":   RedactedValue,
				"Content-Type":   "application/json",
			},
		},
		{
			name:     "nil headers",
			headers:  nil,
			expected: nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := s.SanitizeHeadersMap(tt.headers)

			if tt.expected == nil {
				assert.Nil(t, result)
				return
			}

			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestSanitizeQuery(t *testing.T) {
	s := New()

	tests := []struct {
		name     string
		values   url.Values
		expected string
	}{
		{
			name: "sensitive password",
			values: url.Values{
				"user":     []string{"john"},
				"password": []string{"secret123"},
			},
			expected: "password=******&user=john",
		},
		{
			name: "multiple values for token",
			values: url.Values{
				"token": []string{"abc", "def"},
				"q":     []string{"search"},
			},
			expected: "q=search&token=******&token=******",
		},
		{
			name:     "nil values",
			values:   nil,
			expected: "",
		},
		{
			name: "no sensitive fields",
			values: url.Values{
				"page": []string{"1"},
				"sort": []string{"asc"},
			},
			expected: "page=1&sort=asc",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := s.SanitizeQuery(tt.values)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestIsSensitiveField(t *testing.T) {
	s := New()

	tests := []struct {
		name      string
		fieldName string
		expected  bool
	}{
		{"exact match", "password", true},
		{"case insensitive", "Password", true},
		{"user_password", "user_password", true},
		{"passwordhash lowercase", "passwordhash", true},
		{"passwordhash camelCase", "passwordHash", true}, // Detected via case-insensitive match
		{"api_key", "api_key", true},
		{"apikey", "apikey", true},
		{"bearer_token", "bearer_token", true},
		{"auth_token", "auth_token", true},
		{"client_secret", "client_secret", true},
		{"private_key", "private_key", true},
		{"access_key", "access_key", true},
		{"sessionid", "sessionid", true},
		{"jwt", "jwt", true},
		{"not sensitive", "username", false},
		{"not sensitive", "email", false},
		{"not sensitive", "data", false},
		{"not sensitive", "id", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := s.isSensitiveField(tt.fieldName)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestAddCustomFields(t *testing.T) {
	t.Run("add single field at a time", func(t *testing.T) {
		s := New()
		s.AddSensitiveField("customSecret")
		s.AddSensitiveField("InternalKey")

		input := `{"customSecret":"value123","InternalKey":"key456","normalField":"data"}`
		result := s.SanitizeJSON([]byte(input))

		var resultMap map[string]any
		err := json.Unmarshal([]byte(result), &resultMap)
		assert.NoError(t, err)

		assert.Equal(t, RedactedValue, resultMap["customSecret"])
		assert.Equal(t, RedactedValue, resultMap["InternalKey"])
		assert.Equal(t, "data", resultMap["normalField"])
	})

	t.Run("add multiple fields at once", func(t *testing.T) {
		s := New()
		s.AddSensitiveField("customer_ssn", "credit_card", "tax_id", "bank_account")

		input := `{
			"name":"John",
			"customer_ssn":"123-45-6789",
			"credit_card":"4111-1111-1111-1111",
			"tax_id":"12-3456789",
			"bank_account":"9876543210",
			"email":"john@example.com"
		}`
		result := s.SanitizeJSON([]byte(input))

		var resultMap map[string]any
		err := json.Unmarshal([]byte(result), &resultMap)
		assert.NoError(t, err)

		assert.Equal(t, RedactedValue, resultMap["customer_ssn"])
		assert.Equal(t, RedactedValue, resultMap["credit_card"])
		assert.Equal(t, RedactedValue, resultMap["tax_id"])
		assert.Equal(t, RedactedValue, resultMap["bank_account"])
		assert.Equal(t, "John", resultMap["name"])
		assert.Equal(t, "john@example.com", resultMap["email"])
	})
}

func TestAddCustomHeaders(t *testing.T) {
	t.Run("add single header at a time", func(t *testing.T) {
		s := New()
		s.AddSensitiveHeader("X-Custom-Auth")
		s.AddSensitiveHeader("X-Internal-Token")

		headers := http.Header{
			"X-Custom-Auth":    []string{"custom-value"},
			"X-Internal-Token": []string{"token-value"},
			"Accept":           []string{"application/json"},
		}

		result := s.SanitizeHeaders(headers)

		assert.Equal(t, RedactedValue, result.Get("X-Custom-Auth"))
		assert.Equal(t, RedactedValue, result.Get("X-Internal-Token"))
		assert.Equal(t, "application/json", result.Get("Accept"))
	})

	t.Run("add multiple headers at once", func(t *testing.T) {
		s := New()
		s.AddSensitiveHeader("X-Custom-Auth", "X-Internal-Token", "X-Partner-Key", "X-Secret-Id")

		headers := http.Header{
			"X-Custom-Auth":    []string{"custom-value"},
			"X-Internal-Token": []string{"token-value"},
			"X-Partner-Key":    []string{"partner-123"},
			"X-Secret-Id":      []string{"secret-456"},
			"Accept":           []string{"application/json"},
			"Content-Type":     []string{"text/plain"},
		}

		result := s.SanitizeHeaders(headers)

		assert.Equal(t, RedactedValue, result.Get("X-Custom-Auth"))
		assert.Equal(t, RedactedValue, result.Get("X-Internal-Token"))
		assert.Equal(t, RedactedValue, result.Get("X-Partner-Key"))
		assert.Equal(t, RedactedValue, result.Get("X-Secret-Id"))
		assert.Equal(t, "application/json", result.Get("Accept"))
		assert.Equal(t, "text/plain", result.Get("Content-Type"))
	})
}

func TestComplexNestedStructure(t *testing.T) {
	s := New()

	input := `{
		"users": [
			{
				"name": "john",
				"auth_info": {
					"password": "secret",
					"api_key": "key123",
					"metadata": {
						"created": "2023-01-01",
						"refresh_token": "refresh123"
					}
				}
			},
			{
				"name": "jane",
				"auth_info": {
					"password": "pass456",
					"token": "token789"
				}
			}
		],
		"config": {
			"app_name": "test",
			"secret_key": "app-secret"
		}
	}`

	result := s.SanitizeJSON([]byte(input))

	var resultMap map[string]any
	err := json.Unmarshal([]byte(result), &resultMap)
	assert.NoError(t, err)

	users := resultMap["users"].([]any)
	user1 := users[0].(map[string]any)
	authInfo1 := user1["auth_info"].(map[string]any)
	assert.Equal(t, RedactedValue, authInfo1["password"])
	assert.Equal(t, RedactedValue, authInfo1["api_key"])

	metadata := authInfo1["metadata"].(map[string]any)
	assert.Equal(t, "2023-01-01", metadata["created"])
	assert.Equal(t, RedactedValue, metadata["refresh_token"])

	config := resultMap["config"].(map[string]any)
	assert.Equal(t, "test", config["app_name"])
	assert.Equal(t, RedactedValue, config["secret_key"])
}
