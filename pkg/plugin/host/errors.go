package host

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"

	"google.golang.org/genproto/googleapis/rpc/errdetails"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// ErrHostAPIUnavailable means this wick does not serve the Host API (it
// predates it, no broker connection was offered, or no plugin token is set)
// or does not serve the requested RPC yet.
var ErrHostAPIUnavailable = errors.New("wick host API unavailable")

// ErrPermissionDenied matches every *PermissionDeniedError via errors.Is.
var ErrPermissionDenied = errors.New("wick host API: permission denied")

// Error detail identity carried on a PermissionDenied gRPC status.
const (
	errorDomain       = "wick.host.v1"
	reasonScopeDenied = "SCOPE_DENIED"
	metaScope         = "scope"
	metaItem          = "item"
	metaPlugin        = "plugin"
)

// PermissionDeniedError is a call the plugin is not allowed to make: its scope
// is not granted, or Item (a table, a REST path, a connector op) is blocked or
// not declared.
type PermissionDeniedError struct {
	Scope   string
	Item    string
	Plugin  string
	Message string
}

func (e *PermissionDeniedError) Error() string {
	s := "wick host API: permission denied"
	if e.Scope != "" {
		s += fmt.Sprintf(": scope %q", e.Scope)
	}
	if e.Item != "" {
		s += fmt.Sprintf(" item %q", e.Item)
	}
	if e.Message != "" {
		s += ": " + e.Message
	}
	return s
}

// Is makes errors.Is(err, ErrPermissionDenied) true.
func (e *PermissionDeniedError) Is(target error) bool { return target == ErrPermissionDenied }

// GRPCStatus lets status.Code / status.FromError see PermissionDenied, and is
// what a server returns to put {scope, item, plugin} on the wire.
func (e *PermissionDeniedError) GRPCStatus() *status.Status {
	st := status.New(codes.PermissionDenied, e.Message)
	md := map[string]string{metaScope: e.Scope, metaPlugin: e.Plugin}
	if e.Item != "" {
		md[metaItem] = e.Item
	}
	if withInfo, err := st.WithDetails(&errdetails.ErrorInfo{Reason: reasonScopeDenied, Domain: errorDomain, Metadata: md}); err == nil {
		return withInfo
	}
	return st
}

// fromGRPC turns a gRPC error into this package's error vocabulary:
// PermissionDenied -> *PermissionDeniedError, Unimplemented ->
// ErrHostAPIUnavailable, anything else stays a status error (status.Code works
// on it).
func fromGRPC(err error) error {
	if err == nil {
		return nil
	}
	st, ok := status.FromError(err)
	if !ok {
		return err
	}
	switch st.Code() {
	case codes.PermissionDenied:
		pd := &PermissionDeniedError{Message: st.Message()}
		for _, d := range st.Details() {
			if info, ok := d.(*errdetails.ErrorInfo); ok && info.GetDomain() == errorDomain {
				md := info.GetMetadata()
				pd.Scope, pd.Item, pd.Plugin = md[metaScope], md[metaItem], md[metaPlugin]
			}
		}
		return pd
	case codes.Unimplemented:
		return fmt.Errorf("%w: %s", ErrHostAPIUnavailable, st.Message())
	}
	return err
}

// HTTP door wire format. Every response the Host API serves carries
// HeaderHostAPI (its version); a response without it came from a wick that
// does not serve the Host API. A failed call additionally carries
// HeaderHostAPIError (the gRPC code name) and an ErrorBody JSON body.
const (
	HeaderHostAPI      = "X-Wick-Host-Api"
	HeaderHostAPIError = "X-Wick-Host-Api-Error"
)

// ErrorBody is the JSON body of a failed HTTP door call. Code is the gRPC code
// name (codes.Code.String(), e.g. "PermissionDenied").
type ErrorBody struct {
	Code    string `json:"code"`
	Message string `json:"message"`
	Scope   string `json:"scope,omitempty"`
	Item    string `json:"item,omitempty"`
	Plugin  string `json:"plugin,omitempty"`
}

// HTTPStatus maps a gRPC code to the HTTP status of the HTTP door, so both
// doors answer the same failure the same way.
func HTTPStatus(c codes.Code) int {
	switch c {
	case codes.OK:
		return http.StatusOK
	case codes.InvalidArgument, codes.OutOfRange:
		return http.StatusBadRequest
	case codes.Unauthenticated:
		return http.StatusUnauthorized
	case codes.PermissionDenied:
		return http.StatusForbidden
	case codes.NotFound:
		return http.StatusNotFound
	case codes.AlreadyExists, codes.Aborted:
		return http.StatusConflict
	case codes.FailedPrecondition:
		return http.StatusPreconditionFailed
	case codes.ResourceExhausted:
		return http.StatusTooManyRequests
	case codes.Canceled:
		return 499 // client closed request
	case codes.Unimplemented:
		return http.StatusNotImplemented
	case codes.Unavailable:
		return http.StatusServiceUnavailable
	case codes.DeadlineExceeded:
		return http.StatusGatewayTimeout
	}
	return http.StatusInternalServerError
}

// CodeForHTTPStatus is the inverse of HTTPStatus, used when a failed response
// carries no ErrorBody code.
func CodeForHTTPStatus(s int) codes.Code {
	switch s {
	case http.StatusBadRequest:
		return codes.InvalidArgument
	case http.StatusUnauthorized:
		return codes.Unauthenticated
	case http.StatusForbidden:
		return codes.PermissionDenied
	case http.StatusNotFound:
		return codes.NotFound
	case http.StatusConflict:
		return codes.Aborted
	case http.StatusPreconditionFailed:
		return codes.FailedPrecondition
	case http.StatusTooManyRequests:
		return codes.ResourceExhausted
	case 499:
		return codes.Canceled
	case http.StatusNotImplemented:
		return codes.Unimplemented
	case http.StatusServiceUnavailable:
		return codes.Unavailable
	case http.StatusGatewayTimeout:
		return codes.DeadlineExceeded
	}
	if s >= 200 && s < 300 {
		return codes.OK
	}
	return codes.Unknown
}

func codeByName(name string) (codes.Code, bool) {
	for c := codes.OK; c <= codes.Unauthenticated; c++ {
		if c.String() == name {
			return c, true
		}
	}
	return codes.Unknown, false
}

// WriteHTTPError writes err as a Host API HTTP door failure: status from
// HTTPStatus, both headers, and an ErrorBody. A *PermissionDeniedError keeps
// its scope/item/plugin; ErrHostAPIUnavailable answers Unimplemented.
func WriteHTTPError(w http.ResponseWriter, err error) {
	body := ErrorBody{}
	var pd *PermissionDeniedError
	switch {
	case errors.As(err, &pd):
		body = ErrorBody{Code: codes.PermissionDenied.String(), Message: pd.Message, Scope: pd.Scope, Item: pd.Item, Plugin: pd.Plugin}
	case errors.Is(err, ErrHostAPIUnavailable):
		body = ErrorBody{Code: codes.Unimplemented.String(), Message: err.Error()}
	case errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded):
		st := status.FromContextError(err)
		body = ErrorBody{Code: st.Code().String(), Message: st.Message()}
	default:
		st := status.Convert(err)
		body = ErrorBody{Code: st.Code().String(), Message: st.Message()}
	}
	c, _ := codeByName(body.Code)
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set(HeaderHostAPI, fmt.Sprint(APIVersion))
	w.Header().Set(HeaderHostAPIError, body.Code)
	w.WriteHeader(HTTPStatus(c))
	_ = json.NewEncoder(w).Encode(body)
}

// errorFromHTTP decodes a failed HTTP door response into the same errors the
// gRPC door produces.
func errorFromHTTP(statusCode int, codeHeader string, raw []byte) error {
	var body ErrorBody
	_ = json.Unmarshal(raw, &body)
	c, ok := codeByName(body.Code)
	if !ok {
		if c, ok = codeByName(codeHeader); !ok {
			c = CodeForHTTPStatus(statusCode)
		}
	}
	msg := body.Message
	if msg == "" && body.Code == "" {
		msg = string(raw)
	}
	switch c {
	case codes.PermissionDenied:
		return &PermissionDeniedError{Scope: body.Scope, Item: body.Item, Plugin: body.Plugin, Message: msg}
	case codes.Unimplemented:
		return fmt.Errorf("%w: %s", ErrHostAPIUnavailable, msg)
	}
	return status.Error(c, msg)
}
