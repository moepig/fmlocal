package matchmaking_test

import (
	"context"
	"testing"
	"time"

	"github.com/moepig/flexi"
	appmm "github.com/moepig/fmlocal/internal/app/matchmaking"
	mm "github.com/moepig/fmlocal/internal/domain/matchmaking"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// backfillRS needs teams a single new ticket cannot fill on its own, so a match
// only forms when the backfill request seats the players already in session.
const backfillRS = `{
  "name": "2v2",
  "ruleLanguageVersion": "1.0",
  "algorithm": {"strategy": "exhaustiveSearch", "backfillPriority": "high"},
  "teams": [
    {"name": "red",  "minPlayers": 2, "maxPlayers": 2},
    {"name": "blue", "minPlayers": 2, "maxPlayers": 2}
  ]
}`

// seated is the roster of a 2v2 session with one blue seat empty.
func seated() []flexi.Player {
	return []flexi.Player{
		{ID: "p1", Team: "red"},
		{ID: "p2", Team: "red"},
		{ID: "p3", Team: "blue"},
	}
}

func TestService_BackfillMatchesWithNewTicket(t *testing.T) {
	h := setup(t, backfillRS, false)
	ctx := context.Background()
	bf, err := h.svc.StartMatchBackfill(ctx, appmm.StartMatchBackfillCommand{
		ConfigurationName: "c1", TicketID: "bf1", GameSessionARN: "gs-1", Players: seated(),
	})
	require.NoError(t, err)
	assert.True(t, bf.IsBackfill())
	assert.Equal(t, mm.StatusQueued, bf.Status())

	_, err = h.svc.StartMatchmaking(ctx, appmm.StartMatchmakingCommand{
		ConfigurationName: "c1", TicketID: "t1", Players: []flexi.Player{{ID: "p4"}},
	})
	require.NoError(t, err)
	require.NoError(t, h.svc.Tick(ctx, "c1"))

	t1, err := h.svc.GetTicket("t1")
	require.NoError(t, err)
	assert.Equal(t, mm.StatusCompleted, bf.Status())
	assert.Equal(t, mm.StatusCompleted, t1.Status())
	// The seated players keep their teams and the newcomer takes the free seat.
	assert.Equal(t, "red", bf.PlayerTeam("p1"))
	assert.Equal(t, "blue", bf.PlayerTeam("p3"))
	assert.Equal(t, "blue", t1.PlayerTeam("p4"))
	assert.Contains(t, h.pub.Names(), "MatchmakingSucceeded")
}

func TestService_BackfillSupersedesWaitingRequestForSameSession(t *testing.T) {
	h := setup(t, backfillRS, false)
	ctx := context.Background()
	first, err := h.svc.StartMatchBackfill(ctx, appmm.StartMatchBackfillCommand{
		ConfigurationName: "c1", TicketID: "bf1", GameSessionARN: "gs-1", Players: seated(),
	})
	require.NoError(t, err)
	second, err := h.svc.StartMatchBackfill(ctx, appmm.StartMatchBackfillCommand{
		ConfigurationName: "c1", TicketID: "bf2", GameSessionARN: "gs-1", Players: seated(),
	})
	require.NoError(t, err)

	assert.Equal(t, mm.StatusCancelled, first.Status())
	assert.Equal(t, "Cancelled", first.StatusReason())
	assert.Contains(t, first.StatusMessage(), "Superseded")
	assert.Equal(t, mm.StatusQueued, second.Status())
	// The replaced request is cancelled before the new one starts searching.
	assert.Equal(t, []string{"MatchmakingSearching", "MatchmakingCancelled", "MatchmakingSearching"}, h.pub.Names())

	// The engine dropped the superseded ticket too, so the new one is free to
	// match: two backfill requests never take part in the same match.
	_, err = h.svc.StartMatchmaking(ctx, appmm.StartMatchmakingCommand{
		ConfigurationName: "c1", TicketID: "t1", Players: []flexi.Player{{ID: "p4"}},
	})
	require.NoError(t, err)
	require.NoError(t, h.svc.Tick(ctx, "c1"))
	assert.Equal(t, mm.StatusCompleted, second.Status())
	assert.Equal(t, mm.StatusCancelled, first.Status())
}

func TestService_BackfillWithoutGameSessionCoexists(t *testing.T) {
	h := setup(t, backfillRS, false)
	ctx := context.Background()
	// Without a GameSessionARN there is no session to be the one request of, so
	// the requests simply queue alongside each other.
	first, err := h.svc.StartMatchBackfill(ctx, appmm.StartMatchBackfillCommand{
		ConfigurationName: "c1", TicketID: "bf1", Players: seated(),
	})
	require.NoError(t, err)
	second, err := h.svc.StartMatchBackfill(ctx, appmm.StartMatchBackfillCommand{
		ConfigurationName: "c1", TicketID: "bf2", Players: seated(),
	})
	require.NoError(t, err)
	assert.Equal(t, mm.StatusQueued, first.Status())
	assert.Equal(t, mm.StatusQueued, second.Status())
}

func TestService_BackfillRefusedWhilePreviousAwaitsAcceptance(t *testing.T) {
	h := setup(t, backfillRS, true)
	ctx := context.Background()
	first, err := h.svc.StartMatchBackfill(ctx, appmm.StartMatchBackfillCommand{
		ConfigurationName: "c1", TicketID: "bf1", GameSessionARN: "gs-1", Players: seated(),
	})
	require.NoError(t, err)
	_, err = h.svc.StartMatchmaking(ctx, appmm.StartMatchmakingCommand{
		ConfigurationName: "c1", TicketID: "t1", Players: []flexi.Player{{ID: "p4"}},
	})
	require.NoError(t, err)
	require.NoError(t, h.svc.Tick(ctx, "c1"))
	require.Equal(t, mm.StatusRequiresAcceptance, first.Status())

	// Replacing it now would tear down a live proposal and cancel the sibling
	// ticket in it, so fmlocal refuses instead.
	_, err = h.svc.StartMatchBackfill(ctx, appmm.StartMatchBackfillCommand{
		ConfigurationName: "c1", TicketID: "bf2", GameSessionARN: "gs-1", Players: seated(),
	})
	require.ErrorIs(t, err, mm.ErrBackfillInProgress)
	assert.Equal(t, mm.StatusRequiresAcceptance, first.Status())
	_, err = h.svc.GetTicket("bf2")
	assert.ErrorIs(t, err, mm.ErrTicketNotFound)
}

func TestService_BackfillWithUnknownTeamIsInvalidRequest(t *testing.T) {
	h := setup(t, backfillRS, false)
	_, err := h.svc.StartMatchBackfill(context.Background(), appmm.StartMatchBackfillCommand{
		ConfigurationName: "c1", TicketID: "bf1",
		Players: []flexi.Player{{ID: "p1", Team: "green"}},
	})
	require.ErrorIs(t, err, mm.ErrInvalidRequest)
}

func TestService_BackfillRejectedRosterLeavesPreviousRequestAlone(t *testing.T) {
	h := setup(t, backfillRS, false)
	ctx := context.Background()
	first, err := h.svc.StartMatchBackfill(ctx, appmm.StartMatchBackfillCommand{
		ConfigurationName: "c1", TicketID: "bf1", GameSessionARN: "gs-1", Players: seated(),
	})
	require.NoError(t, err)

	// A request the engine refuses must not cost the caller the one it had.
	_, err = h.svc.StartMatchBackfill(ctx, appmm.StartMatchBackfillCommand{
		ConfigurationName: "c1", TicketID: "bf2", GameSessionARN: "gs-1",
		Players: []flexi.Player{{ID: "p1", Team: "green"}},
	})
	require.ErrorIs(t, err, mm.ErrInvalidRequest)
	assert.Equal(t, mm.StatusQueued, first.Status())
	assert.Equal(t, []string{"MatchmakingSearching"}, h.pub.Names())
}

func TestService_BackfillTimesOutLikeAnyTicket(t *testing.T) {
	h := setup(t, backfillRS, false)
	ctx := context.Background()
	bf, err := h.svc.StartMatchBackfill(ctx, appmm.StartMatchBackfillCommand{
		ConfigurationName: "c1", TicketID: "bf1", GameSessionARN: "gs-1", Players: seated(),
	})
	require.NoError(t, err)
	h.clock.Advance(61 * time.Second) // past the configuration's 60s requestTimeout
	require.NoError(t, h.svc.Tick(ctx, "c1"))
	assert.Equal(t, mm.StatusTimedOut, bf.Status())
	assert.Contains(t, h.pub.Names(), "MatchmakingTimedOut")
}
