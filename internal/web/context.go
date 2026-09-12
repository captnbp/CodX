package web

import (
	"context"
	"encoding/json"
	"net/http"

	"github.com/captnbp/CodX/internal/session"
)

type contextKey string

const sessionKey contextKey = "session"

// WithSession stores the session in the request context.
func WithSession(ctx context.Context, sess *session.Session) context.Context {
	return context.WithValue(ctx, sessionKey, sess)
}

// SessionFromContext retrieves the session from the context, or nil if not
// present.
func SessionFromContext(ctx context.Context) *session.Session {
	val := ctx.Value(sessionKey)
	if val == nil {
		return nil
	}
	sess, ok := val.(*session.Session)
	if !ok {
		return nil
	}
	return sess
}

// writeJSON serializes the value as JSON and writes it to the response.
func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}
