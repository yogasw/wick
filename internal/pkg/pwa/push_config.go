package pwa

import (
	"context"
	"fmt"
	"strings"

	"github.com/SherClockHolmes/webpush-go"
	"github.com/yogasw/wick/internal/entity"
)

const (
	pushConfigOwner      = "pwa_push"
	pushConfigPublicKey  = "vapid_public_key"
	pushConfigPrivateKey = "vapid_private_key"
)

type pushConfigStore interface {
	AppURL() string
	EncryptionKey() string
	EnsureOwned(ctx context.Context, owner string, rows ...entity.Config) error
	GetOwned(owner, key string) string
	SetOwned(ctx context.Context, owner, key, value string) error
}

// EnsurePushConfig seeds PWA Web Push config rows and creates one VAPID
// keypair on first boot. Public/private keys must be generated together,
// so this lives outside the generic per-key config generator.
func EnsurePushConfig(ctx context.Context, cfg pushConfigStore) error {
	if err := cfg.EnsureOwned(ctx, pushConfigOwner,
		entity.Config{
			Key:         pushConfigPublicKey,
			Type:        "text",
			Description: "VAPID public key used by browsers when subscribing to notifications.",
		},
		entity.Config{
			Key:         pushConfigPrivateKey,
			Type:        "text",
			Description: "VAPID private key used by the server to sign outbound notifications.",
			IsSecret:    true,
		},
	); err != nil {
		return err
	}
	if strings.TrimSpace(cfg.GetOwned(pushConfigOwner, pushConfigPublicKey)) != "" &&
		strings.TrimSpace(cfg.GetOwned(pushConfigOwner, pushConfigPrivateKey)) != "" {
		return nil
	}
	privateKey, publicKey, err := webpush.GenerateVAPIDKeys()
	if err != nil {
		return fmt.Errorf("generate VAPID keys: %w", err)
	}
	if err := cfg.SetOwned(ctx, pushConfigOwner, pushConfigPublicKey, publicKey); err != nil {
		return err
	}
	if err := cfg.SetOwned(ctx, pushConfigOwner, pushConfigPrivateKey, privateKey); err != nil {
		return err
	}
	return nil
}
