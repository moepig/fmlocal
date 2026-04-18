package matchmaking_test

import (
	"testing"
	"time"

	mm "github.com/moepig/fmlocal/internal/domain/matchmaking"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func sampleConfig() mm.Configuration {
	return mm.Configuration{
		Name:              "c1",
		ARN:               "arn:aws:gamelift:us-east-1:000000000000:matchmakingconfiguration/c1",
		RuleSetName:       "1v1",
		RuleSetARN:        "arn:aws:gamelift:us-east-1:000000000000:matchmakingruleset/1v1",
		FlexMatchMode:     mm.FlexMatchModeStandalone,
		RequestTimeout:    60 * time.Second,
		AcceptanceTimeout: 10 * time.Second,
	}
}

func TestNewTicket_RequiresIDAndPlayers(t *testing.T) {
	now := time.Unix(1700000000, 0).UTC()
	_, err := mm.NewTicket("", sampleConfig(), []mm.Player{{ID: "p1"}}, now)
	require.Error(t, err)
	_, err = mm.NewTicket("t1", sampleConfig(), nil, now)
	require.Error(t, err)
}

func TestNewTicket_EmitsSearchingStarted(t *testing.T) {
	now := time.Unix(1700000000, 0).UTC()
	tk, err := mm.NewTicket("t1", sampleConfig(), []mm.Player{{ID: "p1"}}, now)
	require.NoError(t, err)
	assert.Equal(t, mm.StatusQueued, tk.Status())
	events := tk.PullEvents()
	require.Len(t, events, 1)
	_, ok := events[0].(mm.EventTicketSearchingStarted)
	assert.True(t, ok)
}

func TestTicket_AssignToProposalThenComplete(t *testing.T) {
	now := time.Unix(1700000000, 0).UTC()
	tk, err := mm.NewTicket("t1", sampleConfig(), []mm.Player{{ID: "p1"}}, now)
	require.NoError(t, err)
	_ = tk.PullEvents()

	require.NoError(t, tk.AssignToProposal("m-1", now))
	assert.Equal(t, mm.StatusRequiresAcceptance, tk.Status())
	evs := tk.PullEvents()
	require.Len(t, evs, 1)
	assigned, ok := evs[0].(mm.EventTicketAssignedToProposal)
	require.True(t, ok)
	assert.Equal(t, mm.MatchID("m-1"), assigned.MatchID())

	tk.AcceptanceCompleted(mm.AcceptanceAccepted, now)
	require.NoError(t, tk.MoveToPlacing("m-1", now))
	assert.Equal(t, mm.StatusPlacing, tk.Status())

	require.NoError(t, tk.Complete(now))
	assert.Equal(t, mm.StatusCompleted, tk.Status())
	evs = tk.PullEvents()
	assert.Len(t, evs, 2, "AcceptanceCompleted + MatchmakingSucceeded queued before Complete")
}

func TestTicket_InvalidTransitionReturnsError(t *testing.T) {
	now := time.Unix(1700000000, 0).UTC()
	tk, _ := mm.NewTicket("t1", sampleConfig(), []mm.Player{{ID: "p1"}}, now)
	_ = tk.PullEvents()

	err := tk.Complete(now) // QUEUED → COMPLETED is illegal
	require.ErrorIs(t, err, mm.ErrInvalidTransition)
}

func TestTicket_RejectFlow(t *testing.T) {
	now := time.Unix(1700000000, 0).UTC()
	tk, _ := mm.NewTicket("t1", sampleConfig(), []mm.Player{{ID: "p1"}}, now)
	_ = tk.PullEvents()
	require.NoError(t, tk.AssignToProposal("m-1", now))
	_ = tk.PullEvents()

	tk.AcceptanceCompleted(mm.AcceptanceRejected, now)
	require.NoError(t, tk.MarkCancelledViaReject(now))
	assert.Equal(t, mm.StatusCancelled, tk.Status())
	evs := tk.PullEvents()
	types := []string{}
	for _, e := range evs {
		types = append(types, e.EventName())
	}
	assert.Equal(t, []string{"AcceptMatchCompleted", "MatchmakingFailed"}, types)
}

func TestTicket_UserCancelFlow(t *testing.T) {
	now := time.Unix(1700000000, 0).UTC()
	tk, _ := mm.NewTicket("t1", sampleConfig(), []mm.Player{{ID: "p1"}}, now)
	_ = tk.PullEvents()

	tk.RequestCancel()
	assert.True(t, tk.CancelRequestedByAPI())
	require.NoError(t, tk.MarkCancelledByAPI(now))
	evs := tk.PullEvents()
	require.Len(t, evs, 1)
	assert.Equal(t, "MatchmakingCancelled", evs[0].EventName())
}

func TestTicket_RecordPlayerAcceptanceEmitsAcceptMatch(t *testing.T) {
	now := time.Unix(1700000000, 0).UTC()
	tk, _ := mm.NewTicket("t1", sampleConfig(), []mm.Player{{ID: "p1"}}, now)
	_ = tk.PullEvents()
	require.NoError(t, tk.AssignToProposal("m-1", now))
	_ = tk.PullEvents()

	require.NoError(t, tk.RecordPlayerAcceptance("p1", true, now))
	evs := tk.PullEvents()
	require.Len(t, evs, 1)
	acc := evs[0].(mm.EventPlayerAcceptanceRecorded)
	assert.Equal(t, mm.PlayerID("p1"), acc.PlayerID())
	assert.True(t, acc.Accepted())
}

func TestTicket_RecordPlayerAcceptanceOutsideProposalRejected(t *testing.T) {
	now := time.Unix(1700000000, 0).UTC()
	tk, _ := mm.NewTicket("t1", sampleConfig(), []mm.Player{{ID: "p1"}}, now)
	_ = tk.PullEvents()
	err := tk.RecordPlayerAcceptance("p1", true, now)
	require.Error(t, err)
}

func TestTicket_RecordPlayerAcceptanceForUnknownPlayerRejected(t *testing.T) {
	now := time.Unix(1700000000, 0).UTC()
	tk, _ := mm.NewTicket("t1", sampleConfig(), []mm.Player{{ID: "p1"}}, now)
	_ = tk.PullEvents()
	require.NoError(t, tk.AssignToProposal("m-1", now))
	_ = tk.PullEvents()
	err := tk.RecordPlayerAcceptance("ghost", true, now)
	require.ErrorIs(t, err, mm.ErrPlayerNotInTicket)
}

func TestTicket_MarkFailed(t *testing.T) {
	now := time.Unix(1700000000, 0).UTC()
	tk, _ := mm.NewTicket("t1", sampleConfig(), []mm.Player{{ID: "p1"}}, now)
	_ = tk.PullEvents()
	require.NoError(t, tk.AssignToProposal("m-1", now))
	_ = tk.PullEvents()

	require.NoError(t, tk.MoveToPlacing("m-1", now))
	require.NoError(t, tk.MarkFailed("reason", "msg", now))
	assert.Equal(t, mm.StatusFailed, tk.Status())
	evs := tk.PullEvents()
	require.Len(t, evs, 1)
	assert.Equal(t, "MatchmakingFailed", evs[0].EventName())
}

func TestTicket_ObserveSearching(t *testing.T) {
	now := time.Unix(1700000000, 0).UTC()
	tk, _ := mm.NewTicket("t1", sampleConfig(), []mm.Player{{ID: "p1"}}, now)
	_ = tk.PullEvents()
	// ObserveSearching just updates status without emitting an event.
	tk.ObserveSearching()
	assert.Equal(t, mm.StatusSearching, tk.Status())
	assert.Empty(t, tk.PullEvents())
}

func TestTicket_MarkTimedOut(t *testing.T) {
	now := time.Unix(1700000000, 0).UTC()
	tk, _ := mm.NewTicket("t1", sampleConfig(), []mm.Player{{ID: "p1"}}, now)
	_ = tk.PullEvents()
	require.NoError(t, tk.MarkTimedOut("TimedOut", "request timed out", now))
	assert.Equal(t, mm.StatusTimedOut, tk.Status())
	evs := tk.PullEvents()
	require.Len(t, evs, 1)
	assert.Equal(t, "MatchmakingTimedOut", evs[0].EventName())
}

func TestTicket_AccessorsCoverageForEvents(t *testing.T) {
	now := time.Unix(1700000000, 0).UTC()
	cfg := sampleConfig()
	tk, _ := mm.NewTicket("t1", cfg, []mm.Player{{ID: "p1"}}, now)
	evs := tk.PullEvents()
	require.Len(t, evs, 1)
	e := evs[0].(mm.EventTicketSearchingStarted)
	assert.Equal(t, mm.TicketID("t1"), e.TicketID())
	assert.Equal(t, cfg.Name, e.ConfigurationName())
	assert.Equal(t, now, e.OccurredAt())
	assert.Equal(t, "MatchmakingSearching", e.EventName())
}

func TestStatus_IsTerminal(t *testing.T) {
	assert.True(t, mm.StatusCompleted.IsTerminal())
	assert.True(t, mm.StatusCancelled.IsTerminal())
	assert.True(t, mm.StatusFailed.IsTerminal())
	assert.True(t, mm.StatusTimedOut.IsTerminal())
	assert.False(t, mm.StatusQueued.IsTerminal())
	assert.False(t, mm.StatusRequiresAcceptance.IsTerminal())
}
