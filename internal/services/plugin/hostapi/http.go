package hostapi

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/reflect/protoregistry"

	"github.com/yogasw/wick/pkg/plugin/host"
	"github.com/yogasw/wick/pkg/plugin/proto/hostpb"
)

// maxBodyBytes caps an HTTP door request body.
const maxBodyBytes = 4 << 20

type rpcFunc func(ctx context.Context, in proto.Message) (proto.Message, error)

func bind[Req proto.Message, Resp proto.Message](f func(context.Context, Req) (Resp, error)) rpcFunc {
	return func(ctx context.Context, in proto.Message) (proto.Message, error) {
		return f(ctx, in.(Req))
	}
}

type httpRoute struct {
	host.Route
	segs  []string
	input protoreflect.MessageType
	fn    rpcFunc
}

// HTTPHandler serves the HTTP door from host.Routes over the same Server the
// gRPC door uses. The door in front of it authenticates the plugin token
// and puts the Caller on the request context (WithCaller); paths are
// accepted with or without the host.CallbackPrefix mount.
func HTTPHandler(s hostpb.HostServer) http.Handler {
	fns := map[string]rpcFunc{
		"Whoami": bind(s.Whoami), "ListMethods": bind(s.ListMethods),
		"ListUsers": bind(s.ListUsers), "GetUser": bind(s.GetUser), "GetUserByEmail": bind(s.GetUserByEmail),
		"ConnectedAccounts": bind(s.ConnectedAccounts),
		"ListSessions":      bind(s.ListSessions), "GetSessionMeta": bind(s.GetSessionMeta), "SendToSession": bind(s.SendToSession),
		"GetTicket": bind(s.GetTicket), "ListTickets": bind(s.ListTickets), "CreateTicket": bind(s.CreateTicket),
		"UpdateTicket": bind(s.UpdateTicket), "AttachSession": bind(s.AttachSession),
		"GetConfig":              bind(s.GetConfig),
		"ListConnectorInstances": bind(s.ListConnectorInstances), "ExecuteConnector": bind(s.ExecuteConnector),
		"Invoke": bind(s.Invoke), "Query": bind(s.Query),
	}
	svc := hostpb.File_host_proto.Services().ByName("Host")
	routes := make([]httpRoute, 0, len(host.Routes))
	for _, r := range host.Routes {
		fn, ok := fns[r.RPC]
		md := svc.Methods().ByName(protoreflect.Name(r.RPC))
		if !ok || md == nil {
			panic("hostapi: route without handler: " + r.RPC)
		}
		mt, err := protoregistry.GlobalTypes.FindMessageByName(md.Input().FullName())
		if err != nil {
			panic("hostapi: " + err.Error())
		}
		routes = append(routes, httpRoute{Route: r, segs: strings.Split(r.Pattern, "/"), input: mt, fn: fn})
	}
	return &httpDoor{routes: routes}
}

type httpDoor struct{ routes []httpRoute }

func (d *httpDoor) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	path := strings.TrimPrefix(r.URL.EscapedPath(), host.CallbackPrefix)
	if path == host.RestPrefix || strings.HasPrefix(path, host.RestPrefix+"/") {
		host.WriteHTTPError(w, notYet("Rest"))
		return
	}
	segs := strings.Split(path, "/")
	var route *httpRoute
	var params map[string]string
	methodMismatch := false
	for i := range d.routes {
		p, ok := matchPath(d.routes[i].segs, segs)
		if !ok {
			continue
		}
		if d.routes[i].Method != r.Method {
			methodMismatch = true
			continue
		}
		route, params = &d.routes[i], p
		break
	}
	if route == nil {
		if methodMismatch {
			host.WriteHTTPError(w, status.Error(codes.Unimplemented, "method "+r.Method+" not allowed on "+path))
			return
		}
		host.WriteHTTPError(w, status.Error(codes.NotFound, "no Host API route "+path))
		return
	}
	in, err := decodeRequest(r, route, params)
	if err != nil {
		host.WriteHTTPError(w, status.Error(codes.InvalidArgument, err.Error()))
		return
	}
	out, err := route.fn(r.Context(), in)
	if err != nil {
		host.WriteHTTPError(w, err)
		return
	}
	body, err := host.MarshalHTTP(out)
	if err != nil {
		host.WriteHTTPError(w, status.Error(codes.Internal, err.Error()))
		return
	}
	if who, ok := out.(*hostpb.WhoamiResponse); ok {
		body = legacyWhoami(r.Context(), body, who)
	}
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set(host.HeaderHostAPI, strconv.Itoa(host.APIVersion))
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(body)
}

// matchPath matches path segments against a pattern, returning the
// unescaped {name} values.
func matchPath(pattern, path []string) (map[string]string, bool) {
	if len(pattern) != len(path) {
		return nil, false
	}
	var params map[string]string
	for i, p := range pattern {
		if strings.HasPrefix(p, "{") && strings.HasSuffix(p, "}") {
			v, err := url.PathUnescape(path[i])
			if err != nil || v == "" {
				return nil, false
			}
			if params == nil {
				params = map[string]string{}
			}
			params[p[1:len(p)-1]] = v
			continue
		}
		if p != path[i] {
			return nil, false
		}
	}
	return params, true
}

// decodeRequest builds the RPC request: the body (non-GET) through the
// shared codec, then query parameters (GET) and path segments by proto field
// name. Path values win over anything the body said.
func decodeRequest(r *http.Request, route *httpRoute, params map[string]string) (proto.Message, error) {
	in := route.input.New().Interface()
	if r.Method != http.MethodGet {
		raw, err := io.ReadAll(io.LimitReader(r.Body, maxBodyBytes+1))
		if err != nil {
			return nil, fmt.Errorf("read body: %w", err)
		}
		if len(raw) > maxBodyBytes {
			return nil, fmt.Errorf("body larger than %d bytes", maxBodyBytes)
		}
		if err := host.UnmarshalHTTP(raw, in); err != nil {
			return nil, fmt.Errorf("decode body: %w", err)
		}
	} else {
		for k, vs := range r.URL.Query() {
			if _, isPath := params[k]; isPath {
				continue
			}
			for _, v := range vs {
				if err := setField(in.ProtoReflect(), k, v); err != nil {
					return nil, err
				}
			}
		}
	}
	for k, v := range params {
		if err := setField(in.ProtoReflect(), k, v); err != nil {
			return nil, err
		}
	}
	return in, nil
}

// setField sets a scalar (or appends to a scalar list) field from its string
// form. Unknown names are ignored, like unknown JSON keys.
func setField(m protoreflect.Message, name, raw string) error {
	fd := m.Descriptor().Fields().ByName(protoreflect.Name(name))
	if fd == nil || fd.IsMap() || fd.Kind() == protoreflect.MessageKind {
		return nil
	}
	var v protoreflect.Value
	switch fd.Kind() {
	case protoreflect.StringKind:
		v = protoreflect.ValueOfString(raw)
	case protoreflect.BoolKind:
		b, err := strconv.ParseBool(raw)
		if err != nil {
			return fmt.Errorf("%s: want a boolean", name)
		}
		v = protoreflect.ValueOfBool(b)
	case protoreflect.Int32Kind, protoreflect.Sint32Kind, protoreflect.Sfixed32Kind:
		n, err := strconv.ParseInt(raw, 10, 32)
		if err != nil {
			return fmt.Errorf("%s: want an integer", name)
		}
		v = protoreflect.ValueOfInt32(int32(n))
	case protoreflect.Int64Kind, protoreflect.Sint64Kind, protoreflect.Sfixed64Kind:
		n, err := strconv.ParseInt(raw, 10, 64)
		if err != nil {
			return fmt.Errorf("%s: want an integer", name)
		}
		v = protoreflect.ValueOfInt64(n)
	case protoreflect.BytesKind:
		v = protoreflect.ValueOfBytes([]byte(raw))
	default:
		return nil
	}
	if fd.IsList() {
		m.Mutable(fd).List().Append(v)
		return nil
	}
	m.Set(fd, v)
	return nil
}

// legacyWhoami adds the callback door's original {"plugin","scopes"} keys
// to the WhoamiResponse body, so plugins that predate the Host API keep
// reading what they always did.
func legacyWhoami(ctx context.Context, body []byte, who *hostpb.WhoamiResponse) []byte {
	var m map[string]any
	if err := json.Unmarshal(body, &m); err != nil {
		return body
	}
	scopes := who.GetScopesAllowed()
	if c, ok := CallerFrom(ctx); ok && c.CallbackScopes != nil {
		scopes = c.CallbackScopes
	}
	if scopes == nil {
		scopes = []string{}
	}
	m["plugin"] = who.GetPluginKey()
	m["scopes"] = scopes
	out, err := json.Marshal(m)
	if err != nil {
		return body
	}
	return out
}
