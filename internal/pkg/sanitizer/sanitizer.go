package sanitizer

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/url"
	"strings"
)

// Sanitizer provides methods to sanitize sensitive data
type Sanitizer struct {
	config *Config
}

// New creates a new Sanitizer with default configuration
func New() *Sanitizer {
	return &Sanitizer{
		config: DefaultConfig(),
	}
}

// NewWithConfig creates a new Sanitizer with custom configuration
func NewWithConfig(config *Config) *Sanitizer {
	return &Sanitizer{
		config: config,
	}
}

// SanitizeJSON sanitizes sensitive fields in JSON data
// Returns sanitized JSON string, or original data if not valid JSON
func (s *Sanitizer) SanitizeJSON(data []byte) string {
	if len(data) == 0 {
		return ""
	}

	var obj any
	if err := json.Unmarshal(data, &obj); err != nil {
		// Not valid JSON, return as-is
		return string(data)
	}

	// Recursively sanitize the object
	sanitized := s.sanitizeValue(obj)

	// Marshal back to JSON with compact format
	marshaled, err := json.Marshal(sanitized)
	if err != nil {
		return string(data)
	}

	result := new(bytes.Buffer)
	if err := json.Compact(result, marshaled); err != nil {
		return string(data)
	}

	return result.String()
}

// SanitizeHeaders sanitizes sensitive headers
// Returns a copy of headers with sensitive values redacted
func (s *Sanitizer) SanitizeHeaders(headers http.Header) http.Header {
	if headers == nil {
		return nil
	}

	sanitized := make(http.Header, len(headers))
	for key, values := range headers {
		if s.isSensitiveHeader(key) {
			values = []string{RedactedValue}
		}

		sanitized[key] = values
	}
	return sanitized
}

// SanitizeHeadersMap sanitizes sensitive headers from map[string]string
// Returns a copy of headers with sensitive values redacted
func (s *Sanitizer) SanitizeHeadersMap(headers map[string]string) map[string]string {
	if headers == nil {
		return nil
	}

	sanitized := make(map[string]string, len(headers))
	for key, value := range headers {
		if s.isSensitiveHeader(key) {
			value = RedactedValue
		}

		sanitized[key] = value
	}
	return sanitized
}

// SanitizeQuery sanitizes sensitive query parameters
// Returns encoded query string with sensitive values redacted
func (s *Sanitizer) SanitizeQuery(values url.Values) string {
	if values == nil {
		return ""
	}

	sanitized := make(url.Values, len(values))
	for key, vals := range values {
		if s.isSensitiveField(key) {
			redacted := make([]string, len(vals))
			for i := range vals {
				redacted[i] = RedactedValue
			}
			sanitized[key] = redacted
			continue
		}

		sanitized[key] = vals
	}

	encoded := sanitized.Encode()
	if encoded == "" {
		return ""
	}

	// Keep redacted placeholders readable by unescaping the encoded value
	redactedEscaped := url.QueryEscape(RedactedValue)
	return strings.ReplaceAll(encoded, redactedEscaped, RedactedValue)
}

// sanitizeValue recursively sanitizes values in maps and slices
func (s *Sanitizer) sanitizeValue(v any) any {
	switch val := v.(type) {
	case map[string]any:
		return s.sanitizeMap(val)
	case []any:
		return s.sanitizeSlice(val)
	default:
		return val
	}
}

// sanitizeMap sanitizes all values in a map
func (s *Sanitizer) sanitizeMap(m map[string]any) map[string]any {
	result := make(map[string]any, len(m))
	for key, value := range m {
		if s.isSensitiveField(key) {
			result[key] = RedactedValue
			continue
		}

		result[key] = s.sanitizeValue(value)
	}
	return result
}

// sanitizeSlice sanitizes all values in a slice
func (s *Sanitizer) sanitizeSlice(slice []any) []any {
	result := make([]any, len(slice))
	for i, value := range slice {
		result[i] = s.sanitizeValue(value)
	}
	return result
}

// isSensitiveField checks if a field name is sensitive
func (s *Sanitizer) isSensitiveField(fieldName string) bool {
	// Check exact match (case-insensitive)
	lowerField := strings.ToLower(fieldName)
	_, exists := s.config.SensitiveFieldNames[lowerField]
	return exists
}

// isSensitiveHeader checks if a header name is sensitive — by exact match
// or by suffix (e.g. any header ending in "-secret-key").
func (s *Sanitizer) isSensitiveHeader(headerName string) bool {
	lowerHeader := strings.ToLower(headerName)
	if _, exists := s.config.SensitiveHeaders[lowerHeader]; exists {
		return true
	}
	for _, suffix := range s.config.SensitiveHeaderSuffixes {
		if strings.HasSuffix(lowerHeader, suffix) {
			return true
		}
	}
	return false
}

// AddSensitiveField adds one or more custom sensitive field names
func (s *Sanitizer) AddSensitiveField(fieldNames ...string) {
	for _, fieldName := range fieldNames {
		s.config.SensitiveFieldNames[strings.ToLower(fieldName)] = struct{}{}
	}
}

// AddSensitiveHeader adds one or more custom sensitive header names
func (s *Sanitizer) AddSensitiveHeader(headerNames ...string) {
	for _, headerName := range headerNames {
		s.config.SensitiveHeaders[strings.ToLower(headerName)] = struct{}{}
	}
}
