package main

// token_cache.go — in-process cache of access tokens minted from a refresh
// token. The plugin cannot write a refreshed token back to wick's config, so
// without this every call would hit Google's token endpoint first. The
// plugin process outlives single calls, so a refreshed token is reused until
// shortly before it expires; a restart costs one refresh.

import (
	"crypto/sha256"
	"encoding/hex"
	"sync"
	"time"

	"github.com/yogasw/wick/pkg/connector"
)

// tokenExpiryMargin retires a cached token this long before Google does, so a
// call never starts with a token that dies mid-flight.
const tokenExpiryMargin = 2 * time.Minute

type cachedToken struct {
	token   string
	expires time.Time
}

var (
	tokenCacheMu sync.Mutex
	tokenCache   = map[string]cachedToken{}
)

// tokenCacheKey identifies one credential by its client and refresh token,
// hashed so the cache never holds the refresh token itself.
func tokenCacheKey(c *connector.Ctx) string {
	rt := c.Cfg("refresh_token")
	if rt == "" {
		return ""
	}
	sum := sha256.Sum256([]byte(c.Cfg("client_id") + "\x00" + rt))
	return hex.EncodeToString(sum[:])
}

// cachedAccessToken returns a still-valid token minted earlier for this
// credential, or "".
func cachedAccessToken(c *connector.Ctx) string {
	key := tokenCacheKey(c)
	if key == "" {
		return ""
	}
	tokenCacheMu.Lock()
	defer tokenCacheMu.Unlock()
	t, ok := tokenCache[key]
	if !ok || time.Now().After(t.expires) {
		delete(tokenCache, key)
		return ""
	}
	return t.token
}

// storeAccessToken remembers a freshly minted token for expiresIn seconds
// (Google's expires_in), minus tokenExpiryMargin.
func storeAccessToken(c *connector.Ctx, token string, expiresIn int) {
	key := tokenCacheKey(c)
	if key == "" || token == "" || expiresIn <= 0 {
		return
	}
	tokenCacheMu.Lock()
	defer tokenCacheMu.Unlock()
	tokenCache[key] = cachedToken{
		token:   token,
		expires: time.Now().Add(time.Duration(expiresIn)*time.Second - tokenExpiryMargin),
	}
}

// forgetAccessToken drops the cached token for this credential after Google
// refused it.
func forgetAccessToken(c *connector.Ctx) {
	key := tokenCacheKey(c)
	if key == "" {
		return
	}
	tokenCacheMu.Lock()
	defer tokenCacheMu.Unlock()
	delete(tokenCache, key)
}
