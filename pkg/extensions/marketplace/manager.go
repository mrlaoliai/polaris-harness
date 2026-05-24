package marketplace

import (
	"context"
)

type DB interface {
	ExecContext(ctx context.Context, query string, args ...any) (any, error)
	QueryContext(ctx context.Context, query string, args ...any) (any, error)
	QueryRowContext(ctx context.Context, query string, args ...any) any
}

type Manager struct {
	db     DB
	mcpMgr any
}

func NewManager(db DB, mcpMgr any) *Manager {
	return &Manager{db: db, mcpMgr: mcpMgr}
}

// ... logic ...
