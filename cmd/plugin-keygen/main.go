// Command plugin-keygen generates an ed25519 keypair for signing wick
// connector plugins. It writes the private key (base64, 0600) to a path and
// prints the public key (base64) for WICK_PLUGIN_PUBKEY / release ldflags.
//
//	go run ./cmd/plugin-keygen [outpath]   # default: ~/.wick/plugin-signing.key
package main

import (
	"fmt"
	"os"
	"path/filepath"

	wickplugin "github.com/yogasw/wick/pkg/plugin"
)

func main() {
	out := defaultKeyPath()
	if len(os.Args) > 1 {
		out = os.Args[1]
	}
	priv, pub := wickplugin.GenerateKeypair()
	if priv == "" {
		fmt.Fprintln(os.Stderr, "keygen failed")
		os.Exit(1)
	}
	if err := os.MkdirAll(filepath.Dir(out), 0o755); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	if err := os.WriteFile(out, []byte(priv), 0o600); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	fmt.Fprintf(os.Stderr, "private key written to %s (keep secret)\n", out)
	fmt.Fprintln(os.Stderr, "public key (set as WICK_PLUGIN_PUBKEY or bake via ldflags):")
	fmt.Println(pub)
}

func defaultKeyPath() string {
	home, err := os.UserHomeDir()
	if err != nil {
		home = "."
	}
	return filepath.Join(home, ".wick", "plugin-signing.key")
}
