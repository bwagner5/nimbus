package logging

import (
	"context"
	"log/slog"
)

type loggingCtxKey struct{}

func FromContext(ctx context.Context) *slog.Logger {
	return ctx.Value(loggingCtxKey{}).(*slog.Logger)
}

func ToContext(ctx context.Context, logger *slog.Logger) context.Context {
	return context.WithValue(ctx, loggingCtxKey{}, logger)
}
