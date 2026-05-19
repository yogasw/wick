package connectors

import (
	"context"
	"strconv"
	"strings"
	"testing"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"

	"github.com/yogasw/wick/internal/configs"
	"github.com/yogasw/wick/internal/enc"
	"github.com/yogasw/wick/internal/entity"
	"github.com/yogasw/wick/internal/pkg/postgres"
	"github.com/yogasw/wick/pkg/connector"
	"github.com/yogasw/wick/pkg/wickdocs"
)

func benchModule(repeat int) connector.Module {
	type Creds struct {
		Token string `wick:"required;secret"`
	}
	type EchoInput struct {
		Password string `wick:"required;secret"`
	}
	filler := strings.Repeat("lorem-ipsum-dolor-sit-amet-1234567890 ", repeat)
	return connector.Module{
		Meta: connector.Meta{Key: "stub", Name: "Stub", Description: "bench"},
		Configs: []entity.Config{
			{Key: "token", Type: "text", IsSecret: true, Required: true},
		},
		Operations: []connector.Operation{
			connector.Op("echo", "Echo", "echo big payload",
				EchoInput{},
				func(c *connector.Ctx) (any, error) {
					return map[string]string{
						"echoed_token":    c.Cfg("token"),
						"echoed_password": c.Input("password"),
						"filler":          filler,
					}, nil
				}, wickdocs.Docs{},
			),
		},
	}
}

func benchSetup(b *testing.B, payloadKB int, encEnabled bool) (*Service, string, ExecuteParams) {
	b.Helper()
	if encEnabled {
		b.Setenv("WICK_ENC_DISABLE", "")
	} else {
		b.Setenv("WICK_ENC_DISABLE", "true")
	}
	encSvc, err := enc.New(testEncKey)
	if err != nil {
		b.Fatalf("enc.New: %v", err)
	}
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{
		Logger: postgres.NewLogLevel("silent"),
	})
	if err != nil {
		b.Fatalf("open sqlite: %v", err)
	}
	sqlDB, _ := db.DB()
	sqlDB.SetMaxOpenConns(1)
	postgres.Migrate(db)
	cfgsSvc := configs.NewService(db)
	if err := cfgsSvc.Bootstrap(context.Background()); err != nil {
		b.Fatalf("configs bootstrap: %v", err)
	}
	svc := NewServiceFromDB(db)
	svc.SetEnc(encSvc)
	svc.SetConfigs(cfgsSvc)
	if err := svc.Bootstrap(context.Background(), []connector.Module{benchModule(payloadKB * 26)}); err != nil {
		b.Fatalf("bootstrap: %v", err)
	}
	rows, _ := svc.List(context.Background())
	if len(rows) == 0 {
		b.Fatal("no row")
	}
	connID := rows[0].ID
	if err := svc.Update(context.Background(), connID, "Stub",
		map[string]string{"token": "super-secret-token-12345"}, false); err != nil {
		b.Fatalf("seed: %v", err)
	}
	return svc, connID, ExecuteParams{
		ConnectorID:  connID,
		OperationKey: "echo",
		Input:        map[string]string{"password": "hunter2-long-pass-67890"},
		Source:       entity.ConnectorRunSourceMCP,
		UserID:       "user-A",
	}
}

func benchExecute(b *testing.B, payloadKB int, encEnabled bool) {
	svc, _, params := benchSetup(b, payloadKB, encEnabled)
	ctx := context.Background()
	b.ResetTimer()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		if _, err := svc.Execute(ctx, params); err != nil {
			b.Fatalf("execute: %v", err)
		}
	}
}

func benchMaskSweep(b *testing.B, responseKB, n int) {
	encSvc, err := enc.New(testEncKey)
	if err != nil {
		b.Fatalf("enc.New: %v", err)
	}
	values := make([]string, n)
	for i := range values {
		values[i] = "sensitive-value-xxxxxxxxxx-" + strconv.Itoa(i)
	}
	body := strings.Repeat("lorem ipsum dolor sit amet 1234567890 ", responseKB*26)
	for _, v := range values {
		body += " " + v
	}
	b.SetBytes(int64(len(body)))
	b.ResetTimer()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		_ = encSvc.Mask(body, values, "user-A")
	}
}

func BenchmarkExecute_EncOff_1KB(b *testing.B)  { benchExecute(b, 1, false) }
func BenchmarkExecute_EncOn_1KB(b *testing.B)   { benchExecute(b, 1, true) }
func BenchmarkExecute_EncOn_10KB(b *testing.B)  { benchExecute(b, 10, true) }
func BenchmarkExecute_EncOn_100KB(b *testing.B) { benchExecute(b, 100, true) }

func BenchmarkMaskSweep_10KB_1value(b *testing.B)    { benchMaskSweep(b, 10, 1) }
func BenchmarkMaskSweep_10KB_10values(b *testing.B)  { benchMaskSweep(b, 10, 10) }
func BenchmarkMaskSweep_10KB_100values(b *testing.B) { benchMaskSweep(b, 10, 100) }
func BenchmarkMaskSweep_100KB_10values(b *testing.B) { benchMaskSweep(b, 100, 10) }
