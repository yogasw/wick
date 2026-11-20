package codemode

import (
	"container/list"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"regexp"
	"strings"
	"sync"
	"time"
)

// Call cache: a script may ask wick to keep a read call's answer for a
// while, so a check that runs often (an approver rule reading a user
// group, a report looking up the same app on every row) does not hit the
// connector each time:
//
//	wick.call(tool, params, { cache: 300 })               // answers, 5 min
//	wick.call(tool, params, { cache: { ok: 300, error: 60 } }) // failures too
//
// Only the script decides, per call; nothing is cached by default. What
// stays safe:
//   - a destructive op is never cached (the option is ignored);
//   - an entry belongs to one caller (Request.CacheScope: user + session),
//     never shared with someone whose access differs;
//   - a hit still passes the caller's access pre-flight (Host.Check), so
//     access taken away stops the cached answer too;
//   - a failure is kept only when it is an answer (not found, forbidden,
//     bad params); timeouts, rate limits, 5xx and approval waits are not;
//   - TTLs are capped, entries are bounded in count and size.

const (
	// CacheMaxTTL and CacheMaxErrorTTL cap what a script may ask for.
	CacheMaxTTL      = time.Hour
	CacheMaxErrorTTL = 10 * time.Minute
	// cacheMaxEntries / cacheMaxBytes bound the whole cache; the oldest
	// entries go first. cacheMaxEntryBytes skips one oversized answer.
	cacheMaxEntries    = 5000
	cacheMaxBytes      = 32 << 20
	cacheMaxEntryBytes = 1 << 20
)

// CacheOpt is a call's cache option, as the script wrote it.
type CacheOpt struct {
	OK    time.Duration
	Error time.Duration
}

// ParseCacheOpt reads the cache option: a number of seconds (answers
// only), true (60s), or {ok, error} in seconds. Anything else, or
// nothing, means no cache.
func ParseCacheOpt(raw json.RawMessage) CacheOpt {
	s := strings.TrimSpace(string(raw))
	if s == "" || s == "null" || s == "false" {
		return CacheOpt{}
	}
	if s == "true" {
		return CacheOpt{OK: time.Minute}
	}
	var n float64
	if json.Unmarshal(raw, &n) == nil {
		return CacheOpt{OK: secs(n, CacheMaxTTL)}
	}
	var o struct {
		OK    *float64 `json:"ok"`
		TTL   *float64 `json:"ttl"`
		Error *float64 `json:"error"`
	}
	if json.Unmarshal(raw, &o) != nil {
		return CacheOpt{}
	}
	var c CacheOpt
	if o.OK != nil {
		c.OK = secs(*o.OK, CacheMaxTTL)
	} else if o.TTL != nil {
		c.OK = secs(*o.TTL, CacheMaxTTL)
	}
	if o.Error != nil {
		c.Error = secs(*o.Error, CacheMaxErrorTTL)
	}
	return c
}

func secs(n float64, maxD time.Duration) time.Duration {
	if n <= 0 {
		return 0
	}
	d := time.Duration(n * float64(time.Second))
	return min(d, maxD)
}

// On reports whether the option caches anything.
func (c CacheOpt) On() bool { return c.OK > 0 || c.Error > 0 }

// transientErr matches failures that say nothing about the answer: the
// next try may well succeed, so they are never kept.
var transientErr = regexp.MustCompile(`(?i)time[sd]? ?out|deadline|rate.?limit|too many|\b429\b|\b5\d\d\b|unavailable|connection|\beof\b|reset by peer|refused|temporar|try again|cancel|while this call was running|the run ended|approv|access request|wick is gone|busy`)

// cacheableError reports whether a failed call's error may be kept.
func cacheableError(msg string) bool {
	return strings.TrimSpace(msg) != "" && !transientErr.MatchString(msg)
}

type cacheEntry struct {
	key     string
	ok      bool
	result  string // the connector's answer text (ok) or the error (failed)
	expires time.Time
	size    int
}

// callCache is the process-wide cache of call answers.
type callCache struct {
	mu    sync.Mutex
	ll    *list.List // front = newest
	m     map[string]*list.Element
	bytes int
	now   func() time.Time
}

func newCallCache() *callCache {
	return &callCache{ll: list.New(), m: map[string]*list.Element{}, now: time.Now}
}

var sharedCache = newCallCache()

// CacheKey names one caller's call: scope, tool_id and params (map keys
// sorted by encoding/json, so the same params give the same key).
func CacheKey(scope, toolID string, params map[string]any) string {
	raw, _ := json.Marshal(params)
	sum := sha256.Sum256([]byte(scope + "\x00" + toolID + "\x00" + string(raw)))
	return hex.EncodeToString(sum[:])
}

func (c *callCache) get(key string) (cacheEntry, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	el, ok := c.m[key]
	if !ok {
		return cacheEntry{}, false
	}
	e := el.Value.(*cacheEntry)
	if !c.now().Before(e.expires) {
		c.remove(el)
		return cacheEntry{}, false
	}
	return *e, true
}

func (c *callCache) put(key string, ok bool, result string, ttl time.Duration) {
	if ttl <= 0 {
		return
	}
	size := len(key) + len(result)
	if size > cacheMaxEntryBytes {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if el, has := c.m[key]; has {
		c.remove(el)
	}
	e := &cacheEntry{key: key, ok: ok, result: result, expires: c.now().Add(ttl), size: size}
	c.m[key] = c.ll.PushFront(e)
	c.bytes += size
	for c.ll.Len() > cacheMaxEntries || c.bytes > cacheMaxBytes {
		c.remove(c.ll.Back())
	}
}

func (c *callCache) remove(el *list.Element) {
	e := el.Value.(*cacheEntry)
	c.ll.Remove(el)
	delete(c.m, e.key)
	c.bytes -= e.size
}

// cachedCall reads the cache for one call; the run then lets a hit
// answer only after the caller's access pre-flight passes again.
func cachedCall(scope string, opt CacheOpt, toolID string, params map[string]any) (key string, e cacheEntry, hit bool) {
	if scope == "" || !opt.On() {
		return "", cacheEntry{}, false
	}
	key = CacheKey(scope, toolID, params)
	e, hit = sharedCache.get(key)
	return key, e, hit
}

// storeCall keeps one call's answer when its option asks for it.
func storeCall(key string, opt CacheOpt, ok bool, txt, errMsg string) {
	if key == "" {
		return
	}
	if ok {
		sharedCache.put(key, true, txt, opt.OK)
		return
	}
	if cacheableError(errMsg) {
		sharedCache.put(key, false, errMsg, opt.Error)
	}
}
