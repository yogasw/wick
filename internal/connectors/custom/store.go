package custom

import (
	"context"
	"errors"
	"fmt"

	"github.com/yogasw/wick/internal/entity"
	"gorm.io/gorm"
)

// ErrKeyTaken is returned when a draft's key collides with an existing
// custom connector or a registered built-in module.
var ErrKeyTaken = errors.New("connector key already in use")

// Store is the gorm-backed persistence for custom connector definitions
// and their MCP import servers. It owns no business rules beyond unique
// keys — validation lives in schema.go, orchestration in service.go.
type Store struct {
	db *gorm.DB
}

func NewStore(db *gorm.DB) *Store { return &Store{db: db} }

// ── definitions ──────────────────────────────────────────────────────

func (s *Store) ListDefs(ctx context.Context) ([]entity.CustomConnector, error) {
	var out []entity.CustomConnector
	err := s.db.WithContext(ctx).Order("created_at ASC").Find(&out).Error
	return out, err
}

func (s *Store) GetDef(ctx context.Context, id string) (*entity.CustomConnector, error) {
	var d entity.CustomConnector
	if err := s.db.WithContext(ctx).First(&d, "id = ?", id).Error; err != nil {
		return nil, err
	}
	return &d, nil
}

func (s *Store) GetDefByKey(ctx context.Context, key string) (*entity.CustomConnector, error) {
	var d entity.CustomConnector
	if err := s.db.WithContext(ctx).First(&d, "key = ?", key).Error; err != nil {
		return nil, err
	}
	return &d, nil
}

func (s *Store) CreateDef(ctx context.Context, d *entity.CustomConnector) error {
	var n int64
	if err := s.db.WithContext(ctx).Model(&entity.CustomConnector{}).
		Where("key = ?", d.Key).Count(&n).Error; err != nil {
		return err
	}
	if n > 0 {
		return fmt.Errorf("%w: %s", ErrKeyTaken, d.Key)
	}
	return s.db.WithContext(ctx).Create(d).Error
}

// UpdateDef rewrites the mutable definition columns. Key is immutable
// after create — it is baked into the registry, the instance rows, and
// the per-def tag name.
func (s *Store) UpdateDef(ctx context.Context, d *entity.CustomConnector) error {
	return s.db.WithContext(ctx).Model(&entity.CustomConnector{}).
		Where("id = ?", d.ID).
		Updates(map[string]any{
			"name":                 d.Name,
			"description":          d.Description,
			"icon":                 d.Icon,
			"source_meta":          d.SourceMeta,
			"configs":              d.Configs,
			"ops":                  d.Ops,
			"single_instance":      d.SingleInstance,
			"allow_session_config": d.AllowSessionConfig,
			"disabled":             d.Disabled,
		}).Error
}

func (s *Store) DeleteDef(ctx context.Context, id string) error {
	return s.db.WithContext(ctx).Delete(&entity.CustomConnector{}, "id = ?", id).Error
}

// ── MCP servers ──────────────────────────────────────────────────────

func (s *Store) ListServers(ctx context.Context) ([]entity.CustomConnectorMCPServer, error) {
	var out []entity.CustomConnectorMCPServer
	err := s.db.WithContext(ctx).Order("created_at ASC").Find(&out).Error
	return out, err
}

func (s *Store) GetServer(ctx context.Context, id string) (*entity.CustomConnectorMCPServer, error) {
	var srv entity.CustomConnectorMCPServer
	if err := s.db.WithContext(ctx).First(&srv, "id = ?", id).Error; err != nil {
		return nil, err
	}
	return &srv, nil
}

func (s *Store) CreateServer(ctx context.Context, srv *entity.CustomConnectorMCPServer) error {
	return s.db.WithContext(ctx).Create(srv).Error
}

func (s *Store) UpdateServer(ctx context.Context, srv *entity.CustomConnectorMCPServer) error {
	return s.db.WithContext(ctx).Save(srv).Error
}

func (s *Store) DeleteServer(ctx context.Context, id string) error {
	return s.db.WithContext(ctx).Delete(&entity.CustomConnectorMCPServer{}, "id = ?", id).Error
}
