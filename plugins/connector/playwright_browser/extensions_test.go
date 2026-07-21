package main

import (
	"bytes"
	"encoding/binary"
	"testing"
)

func TestToZipBytes(t *testing.T) {
	zip := []byte("PK\x03\x04rest-of-zip")

	t.Run("raw zip passes through", func(t *testing.T) {
		got, err := toZipBytes(zip)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if !bytes.Equal(got, zip) {
			t.Errorf("raw zip mangled: %q", got)
		}
	})

	t.Run("crx3 header stripped", func(t *testing.T) {
		header := []byte("some-crx3-header-bytes")
		var buf bytes.Buffer
		buf.WriteString("Cr24")
		_ = binary.Write(&buf, binary.LittleEndian, uint32(3))
		_ = binary.Write(&buf, binary.LittleEndian, uint32(len(header)))
		buf.Write(header)
		buf.Write(zip)

		got, err := toZipBytes(buf.Bytes())
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if !bytes.Equal(got, zip) {
			t.Errorf("crx3 payload wrong: %q", got)
		}
	})

	t.Run("crx2 header stripped", func(t *testing.T) {
		pub := []byte("pubkey")
		sig := []byte("signature")
		var buf bytes.Buffer
		buf.WriteString("Cr24")
		_ = binary.Write(&buf, binary.LittleEndian, uint32(2))
		_ = binary.Write(&buf, binary.LittleEndian, uint32(len(pub)))
		_ = binary.Write(&buf, binary.LittleEndian, uint32(len(sig)))
		buf.Write(pub)
		buf.Write(sig)
		buf.Write(zip)

		got, err := toZipBytes(buf.Bytes())
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if !bytes.Equal(got, zip) {
			t.Errorf("crx2 payload wrong: %q", got)
		}
	})

	t.Run("rejects unknown magic", func(t *testing.T) {
		if _, err := toZipBytes([]byte("NOTACRX-and-not-a-zip")); err == nil {
			t.Error("expected error for unknown archive")
		}
	})

	t.Run("rejects crx with header past EOF", func(t *testing.T) {
		var buf bytes.Buffer
		buf.WriteString("Cr24")
		_ = binary.Write(&buf, binary.LittleEndian, uint32(3))
		_ = binary.Write(&buf, binary.LittleEndian, uint32(9999)) // header len > file
		if _, err := toZipBytes(buf.Bytes()); err == nil {
			t.Error("expected error for oversized crx3 header")
		}
	})
}

func TestValidExtID(t *testing.T) {
	ok := []string{"ublock", "my-ext_1.0", "aabbccddeeffgghhiijjkkllmmnnoopp"}
	bad := []string{"", ".", "..", "../evil", "a/b", "a\\b", "with space"}
	for _, s := range ok {
		if !validExtID(s) {
			t.Errorf("expected %q valid", s)
		}
	}
	for _, s := range bad {
		if validExtID(s) {
			t.Errorf("expected %q invalid", s)
		}
	}
}
