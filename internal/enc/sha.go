package enc

import (
	"crypto/sha256"
	"hash"
)

// newSHA256 is the hash constructor passed to hkdf.New. Lifted out so
// the Service code reads as plain HKDF without the import noise.
func newSHA256() hash.Hash { return sha256.New() }
