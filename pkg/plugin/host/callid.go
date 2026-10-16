package host

import (
	"context"
	"net/http"

	"google.golang.org/grpc/metadata"
)

// Call ids tie a Host API call to the wick request it serves. wick issues one
// per request it sends a plugin (a connector Execute, a proxied tool or
// service request) and remembers who that request runs for; the plugin echoes
// the id on its Host API calls and wick maps it back to the person. The id is
// only a lookup key: wick drops it when the request ends and ignores an id
// another plugin was given, so it cannot be used to borrow an identity.
const (
	// HeaderCallID carries the id on a proxied HTTP request and on HTTP door
	// calls.
	HeaderCallID = "X-Wick-Call-Id"
	// MetadataCallID carries the id as gRPC metadata (Execute, and broker
	// calls back into wick).
	MetadataCallID = "wick-call-id"
)

type callIDKey struct{}

// WithCallID returns ctx carrying the call id of the wick request it serves.
// The plugin SDK sets it on every request context; plugins only need it when
// they start their own context (pass the request's id along).
func WithCallID(ctx context.Context, id string) context.Context {
	if id == "" {
		return ctx
	}
	return context.WithValue(ctx, callIDKey{}, id)
}

// CallIDFrom returns the call id on ctx: the one set by WithCallID, else the
// one wick sent as incoming gRPC metadata (a connector Execute context).
func CallIDFrom(ctx context.Context) string {
	if id, ok := ctx.Value(callIDKey{}).(string); ok && id != "" {
		return id
	}
	if md, ok := metadata.FromIncomingContext(ctx); ok {
		if v := md.Get(MetadataCallID); len(v) > 0 {
			return v[0]
		}
	}
	return ""
}

// CallIDHandler puts the call id wick sent on a proxied request
// (HeaderCallID) on the request context, so Host API calls made with
// r.Context() run for the person behind the request. The plugin SDKs wrap
// their handlers with it.
func CallIDHandler(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if id := r.Header.Get(HeaderCallID); id != "" {
			r = r.WithContext(WithCallID(r.Context(), id))
		}
		next.ServeHTTP(w, r)
	})
}
