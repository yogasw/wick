package host

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"

	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protoreflect"
)

// HTTP door body codec, shared by this client and wick's server. It is plain
// JSON a plugin without generated stubs (Python, Node, curl) can write:
//
//   - keys are proto field names (snake_case); unknown keys are ignored;
//   - a bytes field named X_json carries JSON and travels inline as key X
//     (InvokeRequest.args_json -> "args": {...}, result_json -> "result");
//     decoding also accepts the base64 string under X_json;
//   - int64 is a JSON number (protojson would quote it);
//   - other bytes fields are base64 (only Rest has them, and Rest is a raw
//     passthrough on the HTTP door, never wrapped in this codec);
//   - fields listed in httpFieldNames travel under another key
//     (QueryRequest.args -> "params");
//   - only populated fields are written; an explicit-presence (optional)
//     field set to its zero value is written.
const jsonSuffix = "_json"

// httpFieldNames renames fields whose proto name reads badly on the wire.
var httpFieldNames = map[protoreflect.FullName]string{
	"wick.host.v1.QueryRequest.args": "params",
}

func httpKey(fd protoreflect.FieldDescriptor) string {
	if k, ok := httpFieldNames[fd.FullName()]; ok {
		return k
	}
	name := string(fd.Name())
	if isInlineJSON(fd) {
		return strings.TrimSuffix(name, jsonSuffix)
	}
	return name
}

func isInlineJSON(fd protoreflect.FieldDescriptor) bool {
	return fd.Kind() == protoreflect.BytesKind && !fd.IsList() && strings.HasSuffix(string(fd.Name()), jsonSuffix)
}

// MarshalHTTP encodes m as an HTTP door body.
func MarshalHTTP(m proto.Message) ([]byte, error) {
	v, err := encodeMessage(m.ProtoReflect())
	if err != nil {
		return nil, err
	}
	return json.Marshal(v)
}

// UnmarshalHTTP decodes an HTTP door body into m (reset first).
func UnmarshalHTTP(b []byte, m proto.Message) error {
	proto.Reset(m)
	if len(bytes.TrimSpace(b)) == 0 {
		return nil
	}
	return decodeMessage(b, m.ProtoReflect())
}

func encodeMessage(m protoreflect.Message) (map[string]any, error) {
	out := map[string]any{}
	var err error
	m.Range(func(fd protoreflect.FieldDescriptor, v protoreflect.Value) bool {
		key := httpKey(fd)
		switch {
		case isInlineJSON(fd):
			raw := v.Bytes()
			if len(raw) == 0 {
				return true
			}
			if !json.Valid(raw) {
				err = fmt.Errorf("%s is not valid JSON", fd.FullName())
				return false
			}
			out[key] = json.RawMessage(raw)
		case fd.IsMap():
			mm := map[string]any{}
			v.Map().Range(func(k protoreflect.MapKey, mv protoreflect.Value) bool {
				var ev any
				ev, err = encodeValue(fd.MapValue(), mv)
				mm[k.String()] = ev
				return err == nil
			})
			out[key] = mm
		case fd.IsList():
			l := v.List()
			arr := make([]any, l.Len())
			for i := range arr {
				if arr[i], err = encodeValue(fd, l.Get(i)); err != nil {
					return false
				}
			}
			out[key] = arr
		default:
			out[key], err = encodeValue(fd, v)
		}
		return err == nil
	})
	return out, err
}

func encodeValue(fd protoreflect.FieldDescriptor, v protoreflect.Value) (any, error) {
	switch fd.Kind() {
	case protoreflect.MessageKind, protoreflect.GroupKind:
		return encodeMessage(v.Message())
	case protoreflect.BytesKind:
		return base64.StdEncoding.EncodeToString(v.Bytes()), nil
	case protoreflect.EnumKind:
		return int32(v.Enum()), nil
	}
	return v.Interface(), nil // string, bool, ints, floats: native JSON
}

func decodeMessage(b []byte, m protoreflect.Message) error {
	var obj map[string]json.RawMessage
	if err := json.Unmarshal(b, &obj); err != nil {
		return fmt.Errorf("%s: %w", m.Descriptor().FullName(), err)
	}
	fields := m.Descriptor().Fields()
	for i := 0; i < fields.Len(); i++ {
		fd := fields.Get(i)
		raw, ok := obj[httpKey(fd)]
		if !ok || isNull(raw) {
			if isInlineJSON(fd) {
				// Fallback: protojson base64 under the proto name.
				if s, ok := obj[string(fd.Name())]; ok && !isNull(s) {
					var enc string
					if err := json.Unmarshal(s, &enc); err != nil {
						return fmt.Errorf("%s: %w", fd.FullName(), err)
					}
					dec, err := base64.StdEncoding.DecodeString(enc)
					if err != nil {
						return fmt.Errorf("%s: %w", fd.FullName(), err)
					}
					m.Set(fd, protoreflect.ValueOfBytes(dec))
				}
			}
			continue
		}
		if err := decodeField(raw, m, fd); err != nil {
			return fmt.Errorf("%s: %w", fd.FullName(), err)
		}
	}
	return nil
}

func decodeField(raw json.RawMessage, m protoreflect.Message, fd protoreflect.FieldDescriptor) error {
	switch {
	case isInlineJSON(fd):
		var buf bytes.Buffer
		if err := json.Compact(&buf, raw); err != nil {
			return err
		}
		m.Set(fd, protoreflect.ValueOfBytes(buf.Bytes()))
	case fd.IsMap():
		var obj map[string]json.RawMessage
		if err := json.Unmarshal(raw, &obj); err != nil {
			return err
		}
		mm := m.Mutable(fd).Map()
		for k, rv := range obj {
			v, err := decodeValue(rv, fd.MapValue(), func() protoreflect.Value { return mm.NewValue() })
			if err != nil {
				return err
			}
			mm.Set(protoreflect.ValueOfString(k).MapKey(), v)
		}
	case fd.IsList():
		var arr []json.RawMessage
		if err := json.Unmarshal(raw, &arr); err != nil {
			return err
		}
		l := m.Mutable(fd).List()
		for _, rv := range arr {
			v, err := decodeValue(rv, fd, l.NewElement)
			if err != nil {
				return err
			}
			l.Append(v)
		}
	default:
		v, err := decodeValue(raw, fd, func() protoreflect.Value { return m.NewField(fd) })
		if err != nil {
			return err
		}
		m.Set(fd, v)
	}
	return nil
}

// decodeValue decodes one scalar or message; newMsg yields an empty message
// value for message kinds.
func decodeValue(raw json.RawMessage, fd protoreflect.FieldDescriptor, newMsg func() protoreflect.Value) (protoreflect.Value, error) {
	switch fd.Kind() {
	case protoreflect.MessageKind, protoreflect.GroupKind:
		v := newMsg()
		return v, decodeMessage(raw, v.Message())
	case protoreflect.StringKind:
		var s string
		err := json.Unmarshal(raw, &s)
		return protoreflect.ValueOfString(s), err
	case protoreflect.BoolKind:
		var b bool
		err := json.Unmarshal(raw, &b)
		return protoreflect.ValueOfBool(b), err
	case protoreflect.BytesKind:
		var s string
		if err := json.Unmarshal(raw, &s); err != nil {
			return protoreflect.Value{}, err
		}
		b, err := base64.StdEncoding.DecodeString(s)
		return protoreflect.ValueOfBytes(b), err
	case protoreflect.FloatKind, protoreflect.DoubleKind:
		f, err := strconv.ParseFloat(unquote(raw), 64)
		if fd.Kind() == protoreflect.FloatKind {
			return protoreflect.ValueOfFloat32(float32(f)), err
		}
		return protoreflect.ValueOfFloat64(f), err
	case protoreflect.Int32Kind, protoreflect.Sint32Kind, protoreflect.Sfixed32Kind, protoreflect.EnumKind:
		n, err := strconv.ParseInt(unquote(raw), 10, 32)
		if fd.Kind() == protoreflect.EnumKind {
			return protoreflect.ValueOfEnum(protoreflect.EnumNumber(n)), err
		}
		return protoreflect.ValueOfInt32(int32(n)), err
	case protoreflect.Int64Kind, protoreflect.Sint64Kind, protoreflect.Sfixed64Kind:
		n, err := strconv.ParseInt(unquote(raw), 10, 64)
		return protoreflect.ValueOfInt64(n), err
	case protoreflect.Uint32Kind, protoreflect.Fixed32Kind:
		n, err := strconv.ParseUint(unquote(raw), 10, 32)
		return protoreflect.ValueOfUint32(uint32(n)), err
	case protoreflect.Uint64Kind, protoreflect.Fixed64Kind:
		n, err := strconv.ParseUint(unquote(raw), 10, 64)
		return protoreflect.ValueOfUint64(n), err
	}
	return protoreflect.Value{}, fmt.Errorf("unsupported kind %s", fd.Kind())
}

// unquote accepts a number written bare or as a string ("5"), as protojson
// clients send int64.
func unquote(raw json.RawMessage) string {
	return strings.Trim(strings.TrimSpace(string(raw)), `"`)
}

func isNull(raw json.RawMessage) bool { return string(bytes.TrimSpace(raw)) == "null" }
