package ports

import (
	"context"

	mm "github.com/moepig/fmlocal/internal/domain/matchmaking"
)

type PlayerSnapshot struct {
	PlayerID string
	Team     string
}

type TicketSnapshot struct {
	TicketID  string
	StartTime string
	Players   []PlayerSnapshot
}

type EventSnapshot map[mm.TicketID]TicketSnapshot

type snapshotContextKey struct{}

func WithEventSnapshot(ctx context.Context, snapshot EventSnapshot) context.Context {
	return context.WithValue(ctx, snapshotContextKey{}, snapshot)
}

func EventSnapshotFromContext(ctx context.Context) (EventSnapshot, bool) {
	snapshot, ok := ctx.Value(snapshotContextKey{}).(EventSnapshot)
	return snapshot, ok
}
