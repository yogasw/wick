package worker

import (
	"context"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/yogasw/wick/internal/configs"
	"github.com/yogasw/wick/internal/entity"
	"github.com/yogasw/wick/internal/jobs"
	connectorrunspurge "github.com/yogasw/wick/internal/jobs/connector-runs-purge"
	"github.com/yogasw/wick/internal/manager"
	"github.com/yogasw/wick/internal/pkg/config"
	"github.com/yogasw/wick/internal/pkg/postgres"
	"github.com/yogasw/wick/internal/tags"
	"github.com/yogasw/wick/internal/tools"
	"github.com/yogasw/wick/pkg/job"
	"github.com/yogasw/wick/pkg/tool"

	"github.com/rs/zerolog/log"
)

func NewServer() *Server {
	cfg := config.Load()
	db := postgres.NewGORM(cfg.Database)
	postgres.Migrate(db)

	// Built-in maintenance jobs whose RunFunc needs DB access are
	// registered here, after DB init, so the closure can capture the
	// same handle the worker uses. Must run BEFORE the configs loop
	// below so the job's typed Config rows get seeded too.
	jobs.Register(job.Module{
		Meta: job.Meta{
			Key:         "connector-runs-purge",
			Name:        "Connector Runs Purge",
			Description: "Daily cleanup of connector_runs audit rows older than the retention window.",
			Icon:        "🧹",
			DefaultCron: "0 3 * * *",
			DefaultTags: []tool.DefaultTag{tags.System},
			AutoEnable:  true,
		},
		Configs: entity.StructToConfigs(connectorrunspurge.Config{RetentionDays: 7}),
		Run:     connectorrunspurge.NewRun(db),
	})

	// Reconcile the configs table so job.Ctx.Cfg(...) sees the same
	// cached values the web process uses. Seeds per-tool / per-job
	// rows the same way internal/pkg/api/server.go does.
	configsSvc := configs.NewService(db)
	var extraConfigs []entity.Config
	for _, m := range tools.All() {
		for _, row := range m.Configs {
			row.Owner = m.Meta.Key
			extraConfigs = append(extraConfigs, row)
		}
	}
	for _, jm := range jobs.All() {
		for _, row := range jm.Configs {
			row.Owner = jm.Meta.Key
			extraConfigs = append(extraConfigs, row)
		}
	}
	if err := configsSvc.Bootstrap(context.Background(), extraConfigs...); err != nil {
		log.Fatal().Msgf("configs bootstrap: %s", err.Error())
	}

	jobsSvc := manager.NewServiceFromDB(db)
	jobsSvc.SetConfigReader(configsSvc)

	return &Server{jobsSvc: jobsSvc}
}

type Server struct {
	jobsSvc *manager.Service
}

// Run bootstraps jobs and starts the scheduler loop. It blocks until SIGINT/SIGTERM.
func (s *Server) Run() {
	ctx := context.Background()

	allJobs := jobs.All()
	if err := job.ValidateJobs(allJobs); err != nil {
		log.Fatal().Msgf("%s", err.Error())
	}
	if err := s.jobsSvc.Bootstrap(ctx, allJobs); err != nil {
		log.Fatal().Err(err).Msg("worker: bootstrap jobs")
	}
	log.Info().Msgf("worker: bootstrapped %d job(s)", len(allJobs))

	quit := make(chan os.Signal, 1)
	signal.Notify(quit, os.Interrupt, syscall.SIGTERM)

	ticker := time.NewTicker(60 * time.Second)
	defer ticker.Stop()

	log.Info().Msg("worker: scheduler started (checking every 60s)")

	s.tick(ctx)

	for {
		select {
		case <-ticker.C:
			s.tick(ctx)
		case <-quit:
			log.Info().Msg("worker: shutting down")
			return
		}
	}
}

func (s *Server) tick(ctx context.Context) {
	enabled, err := s.jobsSvc.ListEnabledJobs(ctx)
	if err != nil {
		log.Error().Err(err).Msg("worker: list enabled jobs")
		return
	}

	now := time.Now()
	for _, j := range enabled {
		if j.Schedule == "" {
			continue
		}
		if !cronMatchesNow(j.Schedule, now) {
			continue
		}
		runID, err := s.jobsSvc.RunCron(ctx, j.Key)
		if err != nil {
			log.Warn().Str("job", j.Key).Err(err).Msg("worker: skip run")
			continue
		}
		log.Info().Str("job", j.Key).Str("run_id", runID).Msg("worker: triggered")
	}
}

// ── Cron expression matching ─────────────────────────────────────

func cronMatchesNow(expr string, now time.Time) bool {
	fields := splitFields(expr)
	if len(fields) != 5 {
		return false
	}
	values := [5]int{now.Minute(), now.Hour(), now.Day(), int(now.Month()), int(now.Weekday())}
	ranges := [5][2]int{{0, 59}, {0, 23}, {1, 31}, {1, 12}, {0, 6}}
	for i, field := range fields {
		if !fieldMatches(field, values[i], ranges[i][0], ranges[i][1]) {
			return false
		}
	}
	return true
}

func splitFields(s string) []string {
	var fields []string
	start := -1
	for i, c := range s {
		if c == ' ' || c == '\t' {
			if start >= 0 {
				fields = append(fields, s[start:i])
				start = -1
			}
		} else if start < 0 {
			start = i
		}
	}
	if start >= 0 {
		fields = append(fields, s[start:])
	}
	return fields
}

func fieldMatches(field string, value, min, max int) bool {
	for _, part := range splitComma(field) {
		if partMatches(part, value, min, max) {
			return true
		}
	}
	return false
}

func partMatches(part string, value, min, max int) bool {
	if part == "*" {
		return true
	}
	if len(part) > 2 && part[0] == '*' && part[1] == '/' {
		step := atoi(part[2:])
		if step <= 0 {
			return false
		}
		return (value-min)%step == 0
	}
	if dashIdx := indexOf(part, '-'); dashIdx > 0 {
		slashIdx := indexOf(part, '/')
		var rangeStart, rangeEnd, step int
		if slashIdx > 0 {
			rangeStart = atoi(part[:dashIdx])
			rangeEnd = atoi(part[dashIdx+1 : slashIdx])
			step = atoi(part[slashIdx+1:])
		} else {
			rangeStart = atoi(part[:dashIdx])
			rangeEnd = atoi(part[dashIdx+1:])
			step = 1
		}
		if step <= 0 {
			step = 1
		}
		if value < rangeStart || value > rangeEnd {
			return false
		}
		return (value-rangeStart)%step == 0
	}
	return atoi(part) == value
}

func splitComma(s string) []string {
	var parts []string
	start := 0
	for i := 0; i < len(s); i++ {
		if s[i] == ',' {
			parts = append(parts, s[start:i])
			start = i + 1
		}
	}
	parts = append(parts, s[start:])
	return parts
}

func indexOf(s string, c byte) int {
	for i := 0; i < len(s); i++ {
		if s[i] == c {
			return i
		}
	}
	return -1
}

func atoi(s string) int {
	n := 0
	for _, c := range s {
		if c < '0' || c > '9' {
			return -1
		}
		n = n*10 + int(c-'0')
	}
	return n
}
