package host

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protoreflect"

	"github.com/yogasw/wick/pkg/plugin/proto/hostpb"
)

// maxResponseBytes caps a single HTTP door response read into memory; a
// larger response fails with ResourceExhausted instead of being truncated.
var maxResponseBytes int64 = 64 << 20

type grpcTransport struct{ cc grpc.ClientConnInterface }

func (t *grpcTransport) call(ctx context.Context, rpc string, in, out proto.Message) error {
	if id := CallIDFrom(ctx); id != "" {
		ctx = metadata.AppendToOutgoingContext(ctx, MetadataCallID, id)
	}
	return fromGRPC(t.cc.Invoke(ctx, "/"+hostpb.Host_ServiceDesc.ServiceName+"/"+rpc, in, out))
}

type httpTransport struct {
	base  string
	token string
	hc    *http.Client
}

func (t *httpTransport) call(ctx context.Context, rpc string, in, out proto.Message) error {
	if rpc == "Rest" {
		return t.rest(ctx, in.(*hostpb.RestRequest), out.(*hostpb.RestResponse))
	}
	r, ok := routeByRPC[rpc]
	if !ok {
		return fmt.Errorf("%w: no HTTP route for %s", ErrHostAPIUnavailable, rpc)
	}
	path, used, err := expandPath(r.Pattern, in)
	if err != nil {
		return err
	}
	var body io.Reader
	q := url.Values{}
	if r.Method == http.MethodGet {
		queryParams(in, used, q)
	} else {
		b, err := MarshalHTTP(in)
		if err != nil {
			return status.Error(codes.InvalidArgument, err.Error())
		}
		body = bytes.NewReader(b)
	}
	u := t.base + CallbackPrefix + path
	if len(q) > 0 {
		u += "?" + q.Encode()
	}
	req, err := http.NewRequestWithContext(ctx, r.Method, u, body)
	if err != nil {
		return status.Error(codes.InvalidArgument, err.Error())
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, raw, err := t.do(req)
	if err != nil {
		return err
	}
	if resp.Header.Get(HeaderHostAPI) == "" {
		return legacyResponse(rpc, resp.StatusCode, raw, out)
	}
	if resp.StatusCode < 200 || resp.StatusCode > 299 || resp.Header.Get(HeaderHostAPIError) != "" {
		return errorFromHTTP(resp.StatusCode, resp.Header.Get(HeaderHostAPIError), raw)
	}
	if len(bytes.TrimSpace(raw)) == 0 {
		return nil
	}
	if err := UnmarshalHTTP(raw, out); err != nil {
		return status.Errorf(codes.Internal, "decode %s response: %v", rpc, err)
	}
	if w, ok := out.(*hostpb.WhoamiResponse); ok && w.GetPluginKey() == "" {
		readLegacyWhoami(raw, w) // tolerate a server sending only the old keys
	}
	return nil
}

// rest proxies a wick REST call. The proxied API's own status (404, 422, ...)
// is the result, not an error; only a Host API failure (scope denied, no
// identity) carries HeaderHostAPIError and becomes an error.
func (t *httpTransport) rest(ctx context.Context, in *hostpb.RestRequest, out *hostpb.RestResponse) error {
	method := strings.ToUpper(strings.TrimSpace(in.GetMethod()))
	if method == "" {
		method = http.MethodGet
	}
	p := in.GetPath()
	if !strings.HasPrefix(p, "/") {
		return status.Error(codes.InvalidArgument, "rest path must start with /")
	}
	var body io.Reader
	if len(in.GetBody()) > 0 {
		body = bytes.NewReader(in.GetBody())
	}
	req, err := http.NewRequestWithContext(ctx, method, t.base+CallbackPrefix+RestPrefix+p, body)
	if err != nil {
		return status.Error(codes.InvalidArgument, err.Error())
	}
	for k, v := range in.GetHeaders() {
		// The plugin token is the only credential on this door, and X-Wick-*
		// carries identity wick injects itself; never let a plugin set either.
		// The server strips them too.
		if strings.EqualFold(k, "Authorization") || strings.HasPrefix(http.CanonicalHeaderKey(k), "X-Wick-") {
			continue
		}
		req.Header.Set(k, v)
	}
	resp, raw, err := t.do(req)
	if err != nil {
		return err
	}
	if resp.Header.Get(HeaderHostAPI) == "" {
		return legacyResponse("Rest", resp.StatusCode, raw, out)
	}
	if resp.Header.Get(HeaderHostAPIError) != "" {
		return errorFromHTTP(resp.StatusCode, resp.Header.Get(HeaderHostAPIError), raw)
	}
	out.Status = int32(resp.StatusCode)
	out.Body = raw
	out.Headers = map[string]string{}
	for k, vs := range resp.Header {
		if k == HeaderHostAPI || k == HeaderHostAPIError {
			continue
		}
		out.Headers[k] = strings.Join(vs, ", ") // repeated headers merged per RFC 9110 5.3
	}
	return nil
}

func (t *httpTransport) do(req *http.Request) (*http.Response, []byte, error) {
	req.Header.Set("Authorization", "Bearer "+t.token)
	if id := CallIDFrom(req.Context()); id != "" {
		req.Header.Set(HeaderCallID, id)
	}
	if req.Header.Get("Accept") == "" {
		req.Header.Set("Accept", "application/json")
	}
	resp, err := t.hc.Do(req)
	if err != nil {
		if ctxErr := req.Context().Err(); ctxErr != nil {
			return nil, nil, status.FromContextError(ctxErr).Err()
		}
		return nil, nil, status.Error(codes.Unavailable, err.Error())
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, maxResponseBytes+1))
	if err != nil {
		return nil, nil, status.Error(codes.Unavailable, err.Error())
	}
	if int64(len(raw)) > maxResponseBytes {
		return nil, nil, status.Errorf(codes.ResourceExhausted, "host API response larger than %d bytes", maxResponseBytes)
	}
	return resp, raw, nil
}

// legacyResponse handles a response without HeaderHostAPI: the wick behind the
// token predates the Host API. Its callback door still rejects a bad token
// (401) and answers GET /api/whoami as {"plugin","scopes"}; everything else is
// ErrHostAPIUnavailable.
func legacyResponse(rpc string, code int, raw []byte, out proto.Message) error {
	if code == http.StatusUnauthorized {
		return status.Error(codes.Unauthenticated, strings.TrimSpace(string(raw)))
	}
	if rpc == "Whoami" && code == http.StatusOK && readLegacyWhoami(raw, out.(*hostpb.WhoamiResponse)) {
		return nil // HostApiVersion stays 0: callback door only
	}
	return ErrHostAPIUnavailable
}

// readLegacyWhoami fills w from the callback door's original whoami shape
// {"plugin","scopes"}. It reports whether that shape was present.
func readLegacyWhoami(raw []byte, w *hostpb.WhoamiResponse) bool {
	var legacy struct {
		Plugin string   `json:"plugin"`
		Scopes []string `json:"scopes"`
	}
	if err := json.Unmarshal(raw, &legacy); err != nil || legacy.Plugin == "" {
		return false
	}
	w.PluginKey = legacy.Plugin
	if len(w.ScopesAllowed) == 0 {
		w.ScopesAllowed = legacy.Scopes
	}
	if len(w.ScopesDeclared) == 0 {
		w.ScopesDeclared = legacy.Scopes
	}
	return true
}

// expandPath fills {name} segments of pattern from the fields of in with that
// proto name, path-escaped. An empty value is InvalidArgument.
func expandPath(pattern string, in proto.Message) (string, map[string]bool, error) {
	used := map[string]bool{}
	segs := strings.Split(pattern, "/")
	m := in.ProtoReflect()
	for i, s := range segs {
		if !strings.HasPrefix(s, "{") || !strings.HasSuffix(s, "}") {
			continue
		}
		name := s[1 : len(s)-1]
		fd := m.Descriptor().Fields().ByName(protoreflect.Name(name))
		if fd == nil {
			return "", nil, status.Errorf(codes.Internal, "route %s: no field %q", pattern, name)
		}
		v := m.Get(fd).String()
		if v == "" {
			return "", nil, status.Errorf(codes.InvalidArgument, "%s is required", name)
		}
		segs[i] = url.PathEscape(v)
		used[name] = true
	}
	return strings.Join(segs, "/"), used, nil
}

// queryParams adds every populated scalar field of in that is not already a
// path segment, keyed by proto name. Lists repeat the key; maps are skipped
// (no GET request carries one).
func queryParams(in proto.Message, skip map[string]bool, q url.Values) {
	in.ProtoReflect().Range(func(fd protoreflect.FieldDescriptor, v protoreflect.Value) bool {
		name := string(fd.Name())
		if skip[name] || fd.IsMap() {
			return true
		}
		if fd.IsList() {
			l := v.List()
			for i := 0; i < l.Len(); i++ {
				q.Add(name, scalarString(fd, l.Get(i)))
			}
			return true
		}
		q.Set(name, scalarString(fd, v))
		return true
	})
}

func scalarString(fd protoreflect.FieldDescriptor, v protoreflect.Value) string {
	switch fd.Kind() {
	case protoreflect.BoolKind:
		return strconv.FormatBool(v.Bool())
	case protoreflect.BytesKind:
		return string(v.Bytes())
	}
	return fmt.Sprint(v.Interface())
}
