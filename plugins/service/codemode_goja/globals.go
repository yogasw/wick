package main

import (
	"encoding/base64"
	"fmt"
	"strings"
	"unicode/utf8"

	"github.com/dop251/goja"
)

// pure wraps the Go helpers below as the web globals scripts expect. The
// helpers sit on a hidden object that is gone once this has run.
const pure = `(() => {
  const h = globalThis.__wick_pure;
  delete globalThis.__wick_pure;
  const bytes = (b) => {
    if (b instanceof ArrayBuffer) return b;
    if (ArrayBuffer.isView(b)) return b.buffer.slice(b.byteOffset, b.byteOffset + b.byteLength);
    throw new TypeError("TextDecoder.decode: expected an ArrayBuffer or a typed array");
  };
  const utf8 = (label) => {
    const l = String(label).trim().toLowerCase();
    if (l !== "utf-8" && l !== "utf8" && l !== "unicode-1-1-utf-8") throw new RangeError("only utf-8 is supported, not " + label);
  };
  class TextEncoder {
    get encoding() { return "utf-8"; }
    encode(s = "") { return new Uint8Array(h.encode(String(s))); }
  }
  class TextDecoder {
    constructor(label = "utf-8", opts = {}) {
      utf8(label);
      Object.defineProperty(this, "fatal", { value: !!(opts && opts.fatal) });
      Object.defineProperty(this, "ignoreBOM", { value: !!(opts && opts.ignoreBOM) });
    }
    get encoding() { return "utf-8"; }
    decode(b) { return b === undefined ? "" : h.decode(bytes(b), this.fatal, this.ignoreBOM); }
  }
  for (const [k, v] of Object.entries({ atob: (s) => h.atob(String(s)), btoa: (s) => h.btoa(String(s)), TextEncoder, TextDecoder })) {
    Object.defineProperty(globalThis, k, { value: v, writable: true, configurable: true });
  }
})();`

// bindPure installs atob, btoa, TextEncoder and TextDecoder: pure
// conversions, no IO. Inputs are capped at maxAlloc like the guarded builtins.
func bindPure(vm *goja.Runtime) error {
	fail := func(ctor, msg string) {
		c, _ := goja.AssertConstructor(vm.Get(ctor))
		o, err := c(nil, vm.ToValue(msg))
		if err != nil {
			panic(vm.NewTypeError(msg))
		}
		panic(o)
	}
	big := func(n int) {
		if n > maxAlloc {
			fail("RangeError", fmt.Sprintf("allocation too large (%d > %d)", n, maxAlloc))
		}
	}
	h := vm.NewObject()
	_ = h.Set("btoa", func(s string) string {
		big(len(s))
		b := make([]byte, 0, len(s))
		for _, r := range s {
			if r > 0xFF {
				fail("Error", "InvalidCharacterError: btoa: the string has characters outside of Latin1")
			}
			b = append(b, byte(r))
		}
		return base64.StdEncoding.EncodeToString(b)
	})
	_ = h.Set("atob", func(s string) string {
		big(len(s))
		s = strings.Map(func(r rune) rune {
			if r == ' ' || r == '\t' || r == '\n' || r == '\f' || r == '\r' {
				return -1
			}
			return r
		}, s)
		if len(s)%4 == 0 {
			s = strings.TrimSuffix(strings.TrimSuffix(s, "="), "=")
		}
		b, err := base64.RawStdEncoding.DecodeString(s)
		if err != nil {
			fail("Error", "InvalidCharacterError: atob: the string is not valid base64")
		}
		r := make([]rune, len(b))
		for i, c := range b {
			r[i] = rune(c)
		}
		return string(r)
	})
	_ = h.Set("encode", func(s string) goja.ArrayBuffer {
		big(len(s))
		return vm.NewArrayBuffer([]byte(s))
	})
	_ = h.Set("decode", func(c goja.FunctionCall) goja.Value {
		ab, ok := c.Argument(0).Export().(goja.ArrayBuffer)
		if !ok {
			fail("TypeError", "TextDecoder.decode: expected an ArrayBuffer or a typed array")
		}
		b, fatal, ignoreBOM := ab.Bytes(), c.Argument(1).ToBoolean(), c.Argument(2).ToBoolean()
		big(len(b))
		if !ignoreBOM {
			b = []byte(strings.TrimPrefix(string(b), "\uFEFF"))
		}
		if !utf8.Valid(b) {
			if fatal {
				fail("TypeError", "TextDecoder.decode: the data is not valid utf-8")
			}
			return vm.ToValue(strings.ToValidUTF8(string(b), "\uFFFD"))
		}
		return vm.ToValue(string(b))
	})
	if err := vm.Set("__wick_pure", h); err != nil {
		return err
	}
	_, err := vm.RunString(pure)
	return err
}
