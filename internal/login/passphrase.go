package login

import (
	"crypto/rand"
	"math/big"
	"strings"
)

// Pseudo-word alphabets. Curated to drop visually ambiguous letters
// (q, x, l, w, y as vowel) and stick to phonotactically reasonable
// English-ish output — readable five-syllable words without leaning on
// a canned wordlist.
var (
	consonants = []byte("bcdfghjklmnprstvz")
	vowels     = []byte("aeiou")
)

// GeneratePassphrase returns five dash-separated CVCVC pseudo-words
// drawn from crypto/rand. Each word is 5 chars, total 29 chars; entropy
// ≈ 5 words × log2(17²·5²·17) ≈ 78 bits — plenty for an initial admin
// password and easier to read aloud than a bare base32 string.
//
// Example: "tokel-bavri-zenpu-makdo-flite"
func GeneratePassphrase() string {
	const words = 5
	parts := make([]string, words)
	for i := 0; i < words; i++ {
		parts[i] = pseudoWord()
	}
	return strings.Join(parts, "-")
}

// pseudoWord builds a five-character C-V-C-V-C string using crypto/rand.
// Falls back to deterministic indices if rand.Int errors (should never
// happen on a sane host) so the caller still gets a usable string.
func pseudoWord() string {
	pat := []byte{'c', 'v', 'c', 'v', 'c'}
	out := make([]byte, len(pat))
	for i, kind := range pat {
		switch kind {
		case 'c':
			out[i] = consonants[randIndex(len(consonants))]
		case 'v':
			out[i] = vowels[randIndex(len(vowels))]
		}
	}
	return string(out)
}

func randIndex(n int) int {
	idx, err := rand.Int(rand.Reader, big.NewInt(int64(n)))
	if err != nil {
		return 0
	}
	return int(idx.Int64())
}
