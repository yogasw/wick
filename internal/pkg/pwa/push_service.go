package pwa

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/SherClockHolmes/webpush-go"
	"github.com/yogasw/wick/internal/entity"
	"gorm.io/gorm"
)

type PushService struct {
	repo *pushRepo
	cfg  pushConfigStore
}

func NewPushService(db *gorm.DB, cfg pushConfigStore) *PushService {
	return &PushService{repo: newPushRepo(db), cfg: cfg}
}

type BrowserSubscription struct {
	Endpoint       string  `json:"endpoint"`
	ExpirationTime float64 `json:"expirationTime,omitempty"`
	Keys           struct {
		P256dh string `json:"p256dh"`
		Auth   string `json:"auth"`
	} `json:"keys"`
	DeviceLabel string `json:"deviceLabel,omitempty"`
}

type PushDevice struct {
	ID          string     `json:"id"`
	Endpoint    string     `json:"endpoint"`
	DeviceLabel string     `json:"deviceLabel,omitempty"`
	UserAgent   string     `json:"userAgent,omitempty"`
	LastSeenAt  *time.Time `json:"lastSeenAt,omitempty"`
	CreatedAt   time.Time  `json:"createdAt"`
}

func (s *PushService) PublicKey() string {
	return strings.TrimSpace(s.cfg.GetOwned(pushConfigOwner, pushConfigPublicKey))
}

func (s *PushService) UserPushID(userID string) string {
	mac := hmac.New(sha256.New, []byte(s.cfg.EncryptionKey()))
	_, _ = mac.Write([]byte(userID))
	return "pn_" + base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
}

func (s *PushService) Subscribe(ctx context.Context, userID, userAgent string, req BrowserSubscription) error {
	if userID == "" {
		return errors.New("missing user")
	}
	if strings.TrimSpace(req.Endpoint) == "" || strings.TrimSpace(req.Keys.P256dh) == "" || strings.TrimSpace(req.Keys.Auth) == "" {
		return errors.New("invalid subscription")
	}
	return s.repo.Upsert(ctx, &entity.PushSubscription{
		UserID:      userID,
		Endpoint:    strings.TrimSpace(req.Endpoint),
		P256dh:      strings.TrimSpace(req.Keys.P256dh),
		Auth:        strings.TrimSpace(req.Keys.Auth),
		UserAgent:   strings.TrimSpace(userAgent),
		DeviceLabel: strings.TrimSpace(req.DeviceLabel),
	})
}

func (s *PushService) Unsubscribe(ctx context.Context, userID, endpoint string) error {
	if strings.TrimSpace(endpoint) == "" {
		return errors.New("missing endpoint")
	}
	return s.repo.DisableByEndpoint(ctx, userID, strings.TrimSpace(endpoint))
}

func (s *PushService) Devices(ctx context.Context, userID string) ([]PushDevice, error) {
	rows, err := s.repo.ListActiveByUser(ctx, userID)
	if err != nil {
		return nil, err
	}
	out := make([]PushDevice, 0, len(rows))
	for _, row := range rows {
		out = append(out, PushDevice{
			ID:          row.ID,
			Endpoint:    row.Endpoint,
			DeviceLabel: row.DeviceLabel,
			UserAgent:   row.UserAgent,
			LastSeenAt:  row.LastSeenAt,
			CreatedAt:   row.CreatedAt,
		})
	}
	return out, nil
}

func (s *PushService) SendTest(ctx context.Context, userID, endpoint string) (int, error) {
	payload, _ := json.Marshal(map[string]string{
		"title": "Wick notification",
		"body":  "Notifications are enabled for this device.",
		"url":   "/profile",
	})
	return s.sendToUser(ctx, userID, strings.TrimSpace(endpoint), payload)
}

func (s *PushService) SendToUser(ctx context.Context, userID, title, body, url string) (int, error) {
	title = strings.TrimSpace(title)
	if title == "" {
		title = "Wick notification"
	}
	url = strings.TrimSpace(url)
	if url == "" {
		url = "/"
	}
	payload, _ := json.Marshal(map[string]string{
		"title": title,
		"body":  strings.TrimSpace(body),
		"url":   url,
	})
	return s.sendToUser(ctx, userID, "", payload)
}

// SendToAll broadcasts one notification to every active subscription
// across every user. Wick agents have no per-session owner today —
// lifecycle pushes go out to whoever has notifications enabled and the
// service worker on each receiver decides whether to surface it (e.g.
// suppresses sound if a tab is already focused on the session URL).
//
// Returns the count of successful deliveries; individual send errors
// are joined into the error return so the caller can log but does not
// have to stop on a single dead endpoint.
func (s *PushService) SendToAll(ctx context.Context, title, body, url string) (int, error) {
	title = strings.TrimSpace(title)
	if title == "" {
		title = "Wick notification"
	}
	url = strings.TrimSpace(url)
	if url == "" {
		url = "/"
	}
	payload, _ := json.Marshal(map[string]string{
		"title": title,
		"body":  strings.TrimSpace(body),
		"url":   url,
	})
	rows, err := s.repo.ListAllActive(ctx)
	if err != nil {
		return 0, err
	}
	sent := 0
	var sendErr error
	for _, row := range rows {
		if err := s.send(ctx, row, payload); err != nil {
			sendErr = errors.Join(sendErr, err)
			continue
		}
		sent++
	}
	return sent, sendErr
}

func (s *PushService) sendToUser(ctx context.Context, userID, endpoint string, payload []byte) (int, error) {
	rows, err := s.repo.ListActiveByUser(ctx, userID)
	if err != nil {
		return 0, err
	}
	sent := 0
	var sendErr error
	for _, row := range rows {
		if endpoint != "" && row.Endpoint != endpoint {
			continue
		}
		if err := s.send(ctx, row, payload); err != nil {
			sendErr = errors.Join(sendErr, err)
			continue
		}
		sent++
	}
	return sent, sendErr
}

func (s *PushService) send(ctx context.Context, row entity.PushSubscription, payload []byte) error {
	publicKey := strings.TrimSpace(s.cfg.GetOwned(pushConfigOwner, pushConfigPublicKey))
	privateKey := strings.TrimSpace(s.cfg.GetOwned(pushConfigOwner, pushConfigPrivateKey))
	if publicKey == "" || privateKey == "" {
		return errors.New("push VAPID keys are not configured")
	}
	subscriber := strings.TrimSpace(s.cfg.AppURL())
	if subscriber == "" {
		subscriber = "mailto:admin@example.com"
	}
	resp, err := webpush.SendNotificationWithContext(ctx, payload, &webpush.Subscription{
		Endpoint: row.Endpoint,
		Keys: webpush.Keys{
			P256dh: row.P256dh,
			Auth:   row.Auth,
		},
	}, &webpush.Options{
		Subscriber:      subscriber,
		VAPIDPublicKey:  publicKey,
		VAPIDPrivateKey: privateKey,
		TTL:             30,
	})
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNotFound || resp.StatusCode == http.StatusGone {
		_ = s.repo.DisableEndpoint(ctx, row.Endpoint)
		return fmt.Errorf("push subscription expired: %s", resp.Status)
	}
	if resp.StatusCode >= 300 {
		return fmt.Errorf("push service returned %s", resp.Status)
	}
	return nil
}
