package plugin

import "testing"

func TestSignVerifyRoundTrip(t *testing.T) {
	priv, pub := GenerateKeypair()
	if priv == "" || pub == "" {
		t.Fatal("empty keypair")
	}
	sha := "abc123def456"
	sig, err := signSHA256WithKey(priv, sha)
	if err != nil {
		t.Fatal(err)
	}
	if !VerifySHA256([]string{pub}, sha, sig) {
		t.Fatal("valid signature must verify against its public key")
	}
	if VerifySHA256([]string{pub}, "tampered", sig) {
		t.Fatal("signature must NOT verify against a different sha")
	}
	_, otherPub := GenerateKeypair()
	if VerifySHA256([]string{otherPub}, sha, sig) {
		t.Fatal("signature must NOT verify against an unrelated key")
	}
}

func TestVerifyEmptyInputs(t *testing.T) {
	if VerifySHA256(nil, "x", "y") {
		t.Fatal("no trusted keys → must not verify")
	}
	priv, pub := GenerateKeypair()
	sig, _ := signSHA256WithKey(priv, "x")
	if VerifySHA256([]string{pub}, "x", "") {
		t.Fatal("empty signature → must not verify")
	}
	if VerifySHA256([]string{"!!notbase64!!"}, "x", sig) {
		t.Fatal("garbage key → must not verify (and not panic)")
	}
}
