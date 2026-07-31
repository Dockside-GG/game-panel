package httpapi

import (
	"context"

	"github.com/dockside-gg/game-panel/internal/store"
)

type contextKey string

const (
	sessionContextKey   contextKey = "session"
	requestIDContextKey contextKey = "request-id"
)

func sessionFromContext(ctx context.Context) (store.Session, bool) {
	session, ok := ctx.Value(sessionContextKey).(store.Session)
	return session, ok
}

func requestIDFromContext(ctx context.Context) string {
	value, _ := ctx.Value(requestIDContextKey).(string)
	return value
}
