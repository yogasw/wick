package sanitizer

const (
	RedactedValue = "******"
)

// Config holds sanitization configuration
type Config struct {
	// SensitiveFieldNames contains exact field names to sanitize
	SensitiveFieldNames map[string]struct{}

	// SensitiveHeaders contains exact header names to sanitize
	SensitiveHeaders map[string]struct{}

	// SensitiveHeaderSuffixes catches any header ending with one of
	// these suffixes (e.g. "-secret-key" matches "X-Secret-Key",
	// "X-Secret-Key", etc.)
	SensitiveHeaderSuffixes []string
}

// DefaultConfig returns the default sanitization configuration
func DefaultConfig() *Config {
	return &Config{
		SensitiveFieldNames:     defaultSensitiveFields(),
		SensitiveHeaders:        defaultSensitiveHeaders(),
		SensitiveHeaderSuffixes: defaultSensitiveHeaderSuffixes(),
	}
}

// defaultSensitiveFields returns static list of sensitive field names
func defaultSensitiveFields() map[string]struct{} {
	return map[string]struct{}{
		// Password variations
		"password":         {},
		"passwd":           {},
		"pwd":              {},
		"user_password":    {},
		"userpassword":     {},
		"pass":             {},
		"passphrase":       {},
		"new_password":     {},
		"old_password":     {},
		"current_password": {},
		"password_hash":    {},
		"passwordhash":     {},

		// Secret variations
		"secret":        {},
		"secret_key":    {},
		"secretkey":     {},
		"client_secret": {},
		"clientsecret":  {},
		"app_secret":    {},
		"appsecret":     {},
		"api_secret":    {},
		"apisecret":     {},

		// Token variations
		"token":         {},
		"auth_token":    {},
		"authtoken":     {},
		"access_token":  {},
		"accesstoken":   {},
		"bearer_token":  {},
		"bearertoken":   {},
		"refresh_token": {},
		"refreshtoken":  {},
		"id_token":      {},
		"idtoken":       {},
		"csrf_token":    {},
		"csrftoken":     {},
		"session_token": {},
		"sessiontoken":  {},
		"jwt":           {},
		"jwt_token":     {},
		"oauth_token":   {},
		"oauthtoken":    {},

		// API Key variations
		"api_key":           {},
		"apikey":            {},
		"key":               {},
		"private_key":       {},
		"privatekey":        {},
		"public_key":        {},
		"publickey":         {},
		"access_key":        {},
		"accesskey":         {},
		"secret_access_key": {},

		// Session variations
		"session":    {},
		"session_id": {},
		"sessionid":  {},
		"sessid":     {},

		// Auth variations
		"auth":          {},
		"authorization": {},
		"credentials":   {},
		"credential":    {},

		// Misc sensitive
		"pin":             {},
		"security_answer": {},
		"securityanswer":  {},
	}
}

// defaultSensitiveHeaders returns static list of sensitive headers
func defaultSensitiveHeaders() map[string]struct{} {
	return map[string]struct{}{
		"authorization":       {},
		"x-api-key":           {},
		"x-auth-token":        {},
		"cookie":              {},
		"set-cookie":          {},
		"proxy-authorization": {},
		"www-authenticate":    {},
	}
}

// defaultSensitiveHeaderSuffixes returns suffixes that mark a header as
// sensitive regardless of prefix — e.g. "X-Secret-Key", "ABC-Secret-Key".
func defaultSensitiveHeaderSuffixes() []string {
	return []string{
		"-secret-key",
		"-app-secret",
	}
}
