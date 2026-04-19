package postgres

import (
	"context"
	"time"

	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"
	"gorm.io/gorm/logger"
)

type Logger struct {
	Level zerolog.Level
	Log   zerolog.Logger
}

func NewLogLevel(level string) Logger {
	lvl := zerolog.Disabled
	switch level {
	case "info":
		lvl = zerolog.InfoLevel
	case "warn":
		lvl = zerolog.WarnLevel
	case "debug":
		lvl = zerolog.DebugLevel
	case "error":
		lvl = zerolog.ErrorLevel
	}

	return Logger{
		Log:   log.Level(lvl),
		Level: lvl,
	}
}

func (l Logger) logWithCtx(ctx context.Context, level zerolog.Level) *zerolog.Event {
	if ctx != nil {
		return log.Ctx(ctx).WithLevel(level)
	}
	return l.Log.WithLevel(level)
}

// currently we don't use this function, because Level already defined at struct Logger
func (l Logger) LogMode(level logger.LogLevel) logger.Interface {
	return l
}

func (l Logger) Error(ctx context.Context, msg string, opts ...any) {
	l.logWithCtx(ctx, l.Level).Msgf(msg, opts...)
}

func (l Logger) Warn(ctx context.Context, msg string, opts ...any) {
	l.logWithCtx(ctx, l.Level).Msgf(msg, opts...)
}

func (l Logger) Info(ctx context.Context, msg string, opts ...any) {
	l.logWithCtx(ctx, l.Level).Msgf(msg, opts...)
}

func (l Logger) Trace(ctx context.Context, begin time.Time, f func() (string, int64), err error) {
	if l.Level >= zerolog.Disabled {
		return
	}

	var ze *zerolog.Event = l.logWithCtx(ctx, l.Level)

	if err != nil {
		ze = ze.Err(err)
	}

	sql, rows := f()
	ze.Str("sql", sql).
		Int64("rows", rows).
		Float64("latency", float64(time.Since(begin).Nanoseconds()/1e4)/100.0).
		Msg("database query")
}
