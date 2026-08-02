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

// failBackfillAcceptance matches the backfill request bf1 with a newly started
// ticket, has the seated players accept and the newcomer reject, and ticks
// until the proposal has settled, which leaves bf1 back in the pool.
func failBackfillAcceptance(t *testing.T, h *harness, newTicket mm.TicketID, newcomer string) error {
	t.Helper()
	ctx := context.Background()
	if _, err := h.svc.StartMatchmaking(ctx, appmm.StartMatchmakingCommand{
		ConfigurationName: "c1", TicketID: newTicket, Players: []flexi.Player{{ID: newcomer}},
	}); err != nil {
		return err
	}
	if err := h.svc.Tick(ctx, "c1"); err != nil {
		return err
	}
	if err := h.svc.AcceptMatch(ctx, appmm.AcceptMatchCommand{
		TicketID: "bf1", PlayerIDs: []mm.PlayerID{"p1", "p2", "p3"}, Accepted: true,
	}); err != nil {
		return err
	}
	if err := h.svc.AcceptMatch(ctx, appmm.AcceptMatchCommand{
		TicketID: newTicket, PlayerIDs: []mm.PlayerID{mm.PlayerID(newcomer)}, Accepted: false,
	}); err != nil {
		return err
	}
	return h.svc.Tick(ctx, "c1")
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

func TestService_BackfillStoppedWithStopMatchmaking(t *testing.T) {
	h := setup(t, backfillRS, false)
	ctx := context.Background()
	bf, err := h.svc.StartMatchBackfill(ctx, appmm.StartMatchBackfillCommand{
		ConfigurationName: "c1", TicketID: "bf1", GameSessionARN: "gs-1", Players: seated(),
	})
	require.NoError(t, err)

	// StopMatchmaking is the only way to withdraw a backfill request — GameLift
	// has no StopMatchBackfill — and it ends the ticket exactly as it ends a
	// regular one.
	require.NoError(t, h.svc.StopMatchmaking(ctx, appmm.StopMatchmakingCommand{TicketID: "bf1"}))
	require.NoError(t, h.svc.Tick(ctx, "c1"))
	assert.Equal(t, mm.StatusCancelled, bf.Status())
	assert.Equal(t, "Cancelled", bf.StatusReason())
	assert.Equal(t, "Matchmaking stopped by client", bf.StatusMessage())
	assert.Equal(t, []string{"MatchmakingSearching", "MatchmakingCancelled"}, h.pub.Names())
}

// The one-request-per-session rule binds only the request still outstanding, so
// a session that keeps losing players may ask again once its previous request
// has ended. Every way a request ends is exercised, because the rule is decided
// by the ticket's status alone.
func TestService_BackfillAllowedAgainOncePreviousEnded(t *testing.T) {
	cases := []struct {
		name string
		// end drives the first request, bf1, out of the pool.
		end  func(t *testing.T, h *harness)
		want mm.TicketStatus
	}{
		{
			name: "matched",
			end: func(t *testing.T, h *harness) {
				_, err := h.svc.StartMatchmaking(context.Background(), appmm.StartMatchmakingCommand{
					ConfigurationName: "c1", TicketID: "t1", Players: []flexi.Player{{ID: "p4"}},
				})
				require.NoError(t, err)
				require.NoError(t, h.svc.Tick(context.Background(), "c1"))
			},
			want: mm.StatusCompleted,
		},
		{
			name: "timed out",
			end: func(t *testing.T, h *harness) {
				h.clock.Advance(61 * time.Second)
				require.NoError(t, h.svc.Tick(context.Background(), "c1"))
			},
			want: mm.StatusTimedOut,
		},
		{
			name: "stopped",
			end: func(t *testing.T, h *harness) {
				require.NoError(t, h.svc.StopMatchmaking(context.Background(),
					appmm.StopMatchmakingCommand{TicketID: "bf1"}))
				require.NoError(t, h.svc.Tick(context.Background(), "c1"))
			},
			want: mm.StatusCancelled,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			h := setup(t, backfillRS, false)
			ctx := context.Background()
			first, err := h.svc.StartMatchBackfill(ctx, appmm.StartMatchBackfillCommand{
				ConfigurationName: "c1", TicketID: "bf1", GameSessionARN: "gs-1", Players: seated(),
			})
			require.NoError(t, err)
			tc.end(t, h)
			require.Equal(t, tc.want, first.Status())

			second, err := h.svc.StartMatchBackfill(ctx, appmm.StartMatchBackfillCommand{
				ConfigurationName: "c1", TicketID: "bf2", GameSessionARN: "gs-1", Players: seated(),
			})
			require.NoError(t, err)
			assert.Equal(t, mm.StatusQueued, second.Status())
			// The spent request is left as it ended: a supersession would have
			// rewritten its status message and emitted a second cancellation.
			assert.Equal(t, tc.want, first.Status())
			assert.NotContains(t, first.StatusMessage(), "Superseded")
		})
	}
}

func TestService_BackfillSupersessionIsNotRepeatedOnTheNextTick(t *testing.T) {
	h := setup(t, backfillRS, false)
	ctx := context.Background()
	for _, id := range []mm.TicketID{"bf1", "bf2"} {
		_, err := h.svc.StartMatchBackfill(ctx, appmm.StartMatchBackfillCommand{
			ConfigurationName: "c1", TicketID: id, GameSessionARN: "gs-1", Players: seated(),
		})
		require.NoError(t, err)
	}

	// The supersession retires the earlier request outside the tick loop, which
	// then sees a ticket already CANCELLED on both sides. It must recognise it
	// as settled rather than announce the cancellation a second time.
	require.NoError(t, h.svc.Tick(ctx, "c1"))
	require.NoError(t, h.svc.Tick(ctx, "c1"))
	assert.Equal(t, 1, countName(h.pub.Names(), "MatchmakingCancelled"))
	first, err := h.svc.GetTicket("bf1")
	require.NoError(t, err)
	assert.Equal(t, mm.StatusCancelled, first.Status())
}

func TestService_BackfillDuplicateTicketIDIsRejected(t *testing.T) {
	h := setup(t, backfillRS, false)
	ctx := context.Background()
	_, err := h.svc.StartMatchBackfill(ctx, appmm.StartMatchBackfillCommand{
		ConfigurationName: "c1", TicketID: "bf1", Players: seated(),
	})
	require.NoError(t, err)
	_, err = h.svc.StartMatchBackfill(ctx, appmm.StartMatchBackfillCommand{
		ConfigurationName: "c1", TicketID: "bf1", Players: seated(),
	})
	assert.ErrorIs(t, err, mm.ErrTicketAlreadyExists)

	// Ticket ids are one namespace: a backfill request cannot take the id of a
	// regular ticket either.
	_, err = h.svc.StartMatchmaking(ctx, appmm.StartMatchmakingCommand{
		ConfigurationName: "c1", TicketID: "t1", Players: []flexi.Player{{ID: "p4"}},
	})
	require.NoError(t, err)
	_, err = h.svc.StartMatchBackfill(ctx, appmm.StartMatchBackfillCommand{
		ConfigurationName: "c1", TicketID: "t1", Players: seated(),
	})
	assert.ErrorIs(t, err, mm.ErrTicketAlreadyExists)
}

func TestService_BackfillRequestsDoNotMatchEachOther(t *testing.T) {
	h := setup(t, backfillRS, false)
	ctx := context.Background()
	// Two half-empty sessions whose rosters would satisfy the rule set between
	// them. At most one backfill request takes part in a match, and a match
	// needs a new ticket to join it, so neither is matched into the other's
	// session.
	a, err := h.svc.StartMatchBackfill(ctx, appmm.StartMatchBackfillCommand{
		ConfigurationName: "c1", TicketID: "bfA", GameSessionARN: "gs-1",
		Players: []flexi.Player{{ID: "p1", Team: "red"}, {ID: "p2", Team: "red"}},
	})
	require.NoError(t, err)
	b, err := h.svc.StartMatchBackfill(ctx, appmm.StartMatchBackfillCommand{
		ConfigurationName: "c1", TicketID: "bfB", GameSessionARN: "gs-2",
		Players: []flexi.Player{{ID: "p3", Team: "blue"}, {ID: "p4", Team: "blue"}},
	})
	require.NoError(t, err)

	require.NoError(t, h.svc.Tick(ctx, "c1"))
	assert.Equal(t, mm.StatusQueued, a.Status())
	assert.Equal(t, mm.StatusQueued, b.Status())
	assert.NotContains(t, h.pub.Names(), "PotentialMatchCreated")
}

// A backfill request whose players accepted a proposal that then failed is
// returned to the pool as the request it was: still a backfill request, still
// carrying the seats it reported, and still able to fill them.
func TestService_BackfillReturnedToPoolStaysABackfillRequest(t *testing.T) {
	h := setup(t, backfillRS, true)
	ctx := context.Background()
	bf, err := h.svc.StartMatchBackfill(ctx, appmm.StartMatchBackfillCommand{
		ConfigurationName: "c1", TicketID: "bf1", GameSessionARN: "gs-1", Players: seated(),
	})
	require.NoError(t, err)
	require.NoError(t, failBackfillAcceptance(t, h, "t1", "p4"))

	assert.Equal(t, mm.StatusSearching, bf.Status())
	assert.Equal(t, "ACCEPTANCE_FAILED", bf.StatusReason())
	assert.True(t, bf.IsBackfill())
	assert.Equal(t, "gs-1", bf.GameSessionARN())
	// The seats the request declared survive the round trip.
	assert.Equal(t, "red", bf.PlayerTeam("p1"))
	assert.Equal(t, "blue", bf.PlayerTeam("p3"))
	// One searching event per ticket at the start, plus the re-queue of the
	// backfill request, which AWS announces the same way.
	assert.Equal(t, 3, countName(h.pub.Names(), "MatchmakingSearching"))

	// It fills its empty seat with the next newcomer, as a fresh request would.
	_, err = h.svc.StartMatchmaking(ctx, appmm.StartMatchmakingCommand{
		ConfigurationName: "c1", TicketID: "t2", Players: []flexi.Player{{ID: "p5"}},
	})
	require.NoError(t, err)
	require.NoError(t, h.svc.Tick(ctx, "c1"))
	assert.Equal(t, mm.StatusRequiresAcceptance, bf.Status())
	assert.Equal(t, 2, countName(h.pub.Names(), "PotentialMatchCreated"))
}

func TestService_BackfillReturnedToPoolIsSupersedable(t *testing.T) {
	h := setup(t, backfillRS, true)
	ctx := context.Background()
	first, err := h.svc.StartMatchBackfill(ctx, appmm.StartMatchBackfillCommand{
		ConfigurationName: "c1", TicketID: "bf1", GameSessionARN: "gs-1", Players: seated(),
	})
	require.NoError(t, err)
	require.NoError(t, failBackfillAcceptance(t, h, "t1", "p4"))
	require.Equal(t, mm.StatusSearching, first.Status())

	// Back in the pool the request is waiting again, so a newer one for the
	// same session replaces it rather than being refused.
	second, err := h.svc.StartMatchBackfill(ctx, appmm.StartMatchBackfillCommand{
		ConfigurationName: "c1", TicketID: "bf2", GameSessionARN: "gs-1", Players: seated(),
	})
	require.NoError(t, err)
	assert.Equal(t, mm.StatusCancelled, first.Status())
	assert.Contains(t, first.StatusMessage(), "Superseded")
	assert.Equal(t, mm.StatusQueued, second.Status())
}
