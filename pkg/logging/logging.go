package logging

import (
	"context"
	"io"
	"log/slog"
	"os"

	"github.com/samber/lo"
)

type loggingCtxKey struct{}

func DefaultLogger(verbose bool) *slog.Logger {
	return slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{
		Level: lo.Ternary(verbose, slog.LevelDebug, slog.LevelInfo),
	}))
}

func DefaultFileLogger(verbose bool, file io.Writer) *slog.Logger {
	return slog.New(slog.NewTextHandler(file, &slog.HandlerOptions{
		Level: lo.Ternary(verbose, slog.LevelDebug, slog.LevelInfo),
	}))
}

func NoOpLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, &slog.HandlerOptions{}))
}

func FromContext(ctx context.Context) *slog.Logger {
	return ctx.Value(loggingCtxKey{}).(*slog.Logger)
}

func ToContext(ctx context.Context, logger *slog.Logger) context.Context {
	return context.WithValue(ctx, loggingCtxKey{}, logger)
}
