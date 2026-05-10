package httprest

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestParseQuery(t *testing.T) {
	tests := []struct {
		name    string
		input   string
		want    map[string]string
		wantErr bool
	}{
		{
			name:  "empty string",
			input: "",
			want:  nil,
		},
		{
			name:  "url-encoded string",
			input: "page=1&limit=10",
			want:  map[string]string{"page": "1", "limit": "10"},
		},
		{
			name:  "json object",
			input: `{"page":1,"state":"open"}`,
			want:  map[string]string{"page": "1", "state": "open"},
		},
		{
			name:  "single key",
			input: "q=hello+world",
			want:  map[string]string{"q": "hello world"},
		},
		{
			name:    "invalid json",
			input:   `{"broken":}`,
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			vals, err := parseQuery(tt.input)
			if tt.wantErr {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)
			if tt.want == nil {
				assert.Nil(t, vals)
				return
			}
			for k, v := range tt.want {
				assert.Equal(t, v, vals.Get(k), "key %s", k)
			}
		})
	}
}

func TestResolveContentType(t *testing.T) {
	assert.Equal(t, "application/json", resolveContentType(""))
	assert.Equal(t, "text/plain", resolveContentType("text/plain"))
	assert.Equal(t, "application/json", resolveContentType("  "))
}

func TestTimeoutSecs(t *testing.T) {
	assert.Equal(t, 30, timeoutSecs(newTestCtx(map[string]string{})))
	assert.Equal(t, 30, timeoutSecs(newTestCtx(map[string]string{"timeout_secs": "0"})))
	assert.Equal(t, 60, timeoutSecs(newTestCtx(map[string]string{"timeout_secs": "60"})))
}

func TestTruncate(t *testing.T) {
	assert.Equal(t, "hello", truncate("hello", 10))
	assert.Equal(t, "hell…", truncate("hello world", 4))
	assert.Equal(t, "hello", truncate("hello", 5))
}
