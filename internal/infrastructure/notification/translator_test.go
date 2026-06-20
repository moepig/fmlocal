package notification_test

import (
	"encoding/json"
	"testing"
	"time"

	mm "github.com/moepig/fmlocal/internal/domain/matchmaking"
	"github.com/moepig/fmlocal/internal/app/defaults/idgen"
	"github.com/moepig/fmlocal/internal/infrastructure/notification"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func makeTicket(t *testing.T) *mm.Ticket {
	t.Helper()
	cfg := mm.Configuration{Name: "cfg", ARN: "arn:cfg"}
	tk, err := mm.NewTicket("t1", cfg, []mm.Player{{ID: "p1"}}, time.Date(2026, 4, 18, 10, 0, 0, 0, time.UTC))
	require.NoError(t, err)
	return tk
}

func lookupFor(tk *mm.Ticket) notification.TicketLookup {
	return func(id mm.TicketID) (notification.TicketDetail, bool) {
		if id != tk.ID() {
			return notification.TicketDetail{}, false
		}
		return notification.TicketDetail{
			TicketID:  string(tk.ID()),
			StartTime: tk.StartTime().UTC().Format(notification.ISO8601Millis),
			Players:   []notification.PlayerDetail{{PlayerID: string(tk.Players()[0].ID)}},
		}, true
	}
}

func TestTranslator_RendersSearchingEnvelope(t *testing.T) {
	tk := makeTicket(t)
	events := tk.PullEvents()
	require.Len(t, events, 1)

	tr := notification.NewTranslator(idgen.NewSequence("e-"), notification.EnvelopeSettings{Region: "us-east-1", AccountID: "000000000000"}, lookupFor(tk))
	env, err := tr.Render(events[0])
	require.NoError(t, err)
	assert.Equal(t, "aws.gamelift", env.Source)
	assert.Equal(t, "GameLift Matchmaking Event", env.DetailType)
	// Timestamps use ISO-8601 with millisecond precision, matching AWS.
	assert.Equal(t, "2026-04-18T10:00:00.000Z", env.Time)
	assert.Equal(t, "MatchmakingSearching", env.Detail.Type)
	require.Len(t, env.Detail.Tickets, 1)
	assert.Equal(t, "t1", env.Detail.Tickets[0].TicketID)
	assert.Equal(t, []string{"arn:aws:gamelift:us-east-1:000000000000:matchmakingconfiguration/cfg"}, env.Resources)
	assert.Equal(t, "NOT_AVAILABLE", env.Detail.EstimatedWaitMillis)

	// Confirm it serializes as the bare string AWS emits.
	raw, err := tr.Marshal(events[0])
	require.NoError(t, err)
	var out struct {
		Detail struct {
			EstimatedWaitMillis any `json:"estimatedWaitMillis"`
		} `json:"detail"`
	}
	require.NoError(t, json.Unmarshal(raw, &out))
	assert.Equal(t, "NOT_AVAILABLE", out.Detail.EstimatedWaitMillis)
}

func newTranslator(t *testing.T) (*notification.Translator, *mm.Ticket) {
	t.Helper()
	tk := makeTicket(t)
	tr := notification.NewTranslator(
		idgen.NewSequence("e-"),
		notification.EnvelopeSettings{Region: "us-east-1", AccountID: "000000000000"},
		lookupFor(tk),
	)
	return tr, tk
}

func TestTranslator_AllEventTypes(t *testing.T) {
	now := time.Date(2026, 4, 18, 10, 0, 0, 0, time.UTC)
	cfg := mm.Configuration{Name: "cfg", ARN: "arn:cfg"}

	cases := []struct {
		name    string
		setup   func(*mm.Ticket) mm.Event
		wantTyp string
	}{
		{
			name: "PotentialMatchCreated",
			setup: func(tk *mm.Ticket) mm.Event {
				return mm.NewPotentialMatchCreated("cfg", "m-1", []mm.TicketID{tk.ID()}, nil, now)
			},
			wantTyp: "PotentialMatchCreated",
		},
		{
			name: "AcceptMatch",
			setup: func(tk *mm.Ticket) mm.Event {
				return mm.NewAcceptMatch("cfg", "m-1", []mm.TicketID{tk.ID()}, []mm.PlayerID{"p1"}, true, now)
			},
			wantTyp: "AcceptMatch",
		},
		{
			name: "AcceptMatchCompleted",
			setup: func(tk *mm.Ticket) mm.Event {
				return mm.NewAcceptMatchCompleted("cfg", "m-1", []mm.TicketID{tk.ID()}, mm.AcceptanceAccepted, now)
			},
			wantTyp: "AcceptMatchCompleted",
		},
		{
			name: "MatchmakingSucceeded",
			setup: func(tk *mm.Ticket) mm.Event {
				return mm.NewMatchmakingSucceeded("cfg", "m-1", []mm.TicketID{tk.ID()}, now)
			},
			wantTyp: "MatchmakingSucceeded",
		},
		{
			name: "MatchmakingFailed",
			setup: func(tk *mm.Ticket) mm.Event {
				_ = tk.PullEvents()
				_ = tk.AssignToProposal("m-1", now)
				_ = tk.PullEvents()
				_ = tk.MoveToPlacing("m-1", now)
				_ = tk.MarkFailed("Rejected", "rejected", now)
				return tk.PullEvents()[0]
			},
			wantTyp: "MatchmakingFailed",
		},
		{
			name: "MatchmakingTimedOut",
			setup: func(tk *mm.Ticket) mm.Event {
				_ = tk.PullEvents()
				_ = tk.MarkTimedOut("TimedOut", "timed out", now)
				return tk.PullEvents()[0]
			},
			wantTyp: "MatchmakingTimedOut",
		},
		{
			name: "MatchmakingCancelled",
			setup: func(tk *mm.Ticket) mm.Event {
				_ = tk.PullEvents()
				tk.RequestCancel()
				_ = tk.MarkCancelledByAPI(now)
				return tk.PullEvents()[0]
			},
			wantTyp: "MatchmakingCancelled",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			tk, err := mm.NewTicket("t1", cfg, []mm.Player{{ID: "p1"}}, now)
			require.NoError(t, err)
			ev := tc.setup(tk)
			tr := notification.NewTranslator(
				idgen.NewSequence("e-"),
				notification.EnvelopeSettings{Region: "us-east-1", AccountID: "000000000000"},
				lookupFor(tk),
			)
			env, err := tr.Render(ev)
			require.NoError(t, err)
			assert.Equal(t, tc.wantTyp, env.Detail.Type)
			assert.Equal(t, "aws.gamelift", env.Source)
		})
	}
}

func TestTranslator_CancelledMatchesAWSReasonAndMessage(t *testing.T) {
	now := time.Date(2026, 4, 18, 10, 0, 0, 0, time.UTC)
	cfg := mm.Configuration{Name: "cfg", ARN: "arn:cfg"}
	tk, err := mm.NewTicket("t1", cfg, []mm.Player{{ID: "p1"}}, now)
	require.NoError(t, err)
	_ = tk.PullEvents()
	tk.RequestCancel()
	require.NoError(t, tk.MarkCancelledByAPI(now))
	ev := tk.PullEvents()[0]

	tr := notification.NewTranslator(idgen.NewSequence("e-"),
		notification.EnvelopeSettings{Region: "us-east-1", AccountID: "000000000000"}, lookupFor(tk))
	env, err := tr.Render(ev)
	require.NoError(t, err)
	assert.Equal(t, "MatchmakingCancelled", env.Detail.Type)
	assert.Equal(t, "Cancelled", env.Detail.Reason)
	assert.Equal(t, "Cancelled by request.", env.Detail.Message)
}

func TestTranslator_RendersRuleEvaluationMetrics(t *testing.T) {
	now := time.Date(2026, 4, 18, 10, 0, 0, 0, time.UTC)
	metrics := []mm.RuleEvaluationMetric{{RuleName: "FairSkill", PassedCount: 3, FailedCount: 1}}

	render := func(ev mm.Event, tk *mm.Ticket) notification.Detail {
		tr := notification.NewTranslator(idgen.NewSequence("e-"),
			notification.EnvelopeSettings{Region: "us-east-1", AccountID: "000000000000"}, lookupFor(tk))
		env, err := tr.Render(ev)
		require.NoError(t, err)
		return env.Detail
	}
	assertFairSkill := func(t *testing.T, d notification.Detail) {
		t.Helper()
		require.Len(t, d.RuleEvaluationMetric, 1)
		assert.Equal(t, "FairSkill", d.RuleEvaluationMetric[0].RuleName)
		assert.Equal(t, 3, d.RuleEvaluationMetric[0].PassedCount)
		assert.Equal(t, 1, d.RuleEvaluationMetric[0].FailedCount)
	}

	t.Run("PotentialMatchCreated", func(t *testing.T) {
		tk := makeTicket(t)
		ev := mm.NewPotentialMatchCreated("cfg", "m-1", []mm.TicketID{tk.ID()}, metrics, now)
		assertFairSkill(t, render(ev, tk))
	})
	t.Run("MatchmakingTimedOut", func(t *testing.T) {
		tk := makeTicket(t)
		_ = tk.PullEvents()
		tk.SetRuleMetrics(metrics)
		require.NoError(t, tk.MarkTimedOut("TimedOut", "timed out", now))
		assertFairSkill(t, render(tk.PullEvents()[0], tk))
	})
	t.Run("MatchmakingCancelled", func(t *testing.T) {
		tk := makeTicket(t)
		_ = tk.PullEvents()
		tk.SetRuleMetrics(metrics)
		tk.RequestCancel()
		require.NoError(t, tk.MarkCancelledByAPI(now))
		assertFairSkill(t, render(tk.PullEvents()[0], tk))
	})
}

func TestTranslator_AttachesGameSessionInfo(t *testing.T) {
	now := time.Date(2026, 4, 18, 10, 0, 0, 0, time.UTC)

	render := func(ev mm.Event, tk *mm.Ticket) notification.Detail {
		tr := notification.NewTranslator(idgen.NewSequence("e-"),
			notification.EnvelopeSettings{Region: "us-east-1", AccountID: "000000000000"}, lookupFor(tk))
		env, err := tr.Render(ev)
		require.NoError(t, err)
		return env.Detail
	}

	t.Run("MatchmakingSearching carries the roster, no connection details", func(t *testing.T) {
		tk := makeTicket(t)
		d := render(tk.PullEvents()[0], tk)
		require.NotNil(t, d.GameSessionInfo)
		require.Len(t, d.GameSessionInfo.Players, 1)
		assert.Equal(t, "p1", d.GameSessionInfo.Players[0].PlayerID)
		// STANDALONE creates no game session.
		assert.Empty(t, d.GameSessionInfo.IPAddress)
		assert.Zero(t, d.GameSessionInfo.Port)
		assert.Empty(t, d.GameSessionInfo.GameSessionARN)
		// matchId only appears on MatchmakingSucceeded's gameSessionInfo.
		assert.Empty(t, d.GameSessionInfo.MatchID)
	})

	t.Run("MatchmakingSucceeded sets matchId inside gameSessionInfo", func(t *testing.T) {
		tk := makeTicket(t)
		_ = tk.PullEvents()
		ev := mm.NewMatchmakingSucceeded("cfg", "m-1", []mm.TicketID{tk.ID()}, now)
		d := render(ev, tk)
		require.NotNil(t, d.GameSessionInfo)
		assert.Equal(t, "m-1", d.GameSessionInfo.MatchID)
		require.Len(t, d.GameSessionInfo.Players, 1)
		assert.Equal(t, "p1", d.GameSessionInfo.Players[0].PlayerID)
		assert.Empty(t, d.GameSessionInfo.IPAddress)
	})

	t.Run("AcceptMatch reflects accepted flag in gameSessionInfo players", func(t *testing.T) {
		tk := makeTicket(t)
		_ = tk.PullEvents()
		ev := mm.NewAcceptMatch("cfg", "m-1", []mm.TicketID{tk.ID()}, []mm.PlayerID{"p1"}, false, now)
		d := render(ev, tk)
		require.NotNil(t, d.GameSessionInfo)
		require.Len(t, d.GameSessionInfo.Players, 1)
		require.NotNil(t, d.GameSessionInfo.Players[0].Accepted)
		assert.False(t, *d.GameSessionInfo.Players[0].Accepted)
	})
}

func TestTranslator_GameSessionInfoSerializesPlayers(t *testing.T) {
	tk := makeTicket(t)
	tr := notification.NewTranslator(idgen.NewSequence("e-"),
		notification.EnvelopeSettings{Region: "us-east-1", AccountID: "000000000000"}, lookupFor(tk))
	raw, err := tr.Marshal(tk.PullEvents()[0])
	require.NoError(t, err)
	var out struct {
		Detail struct {
			GameSessionInfo struct {
				Players []struct {
					PlayerID string `json:"playerId"`
				} `json:"players"`
			} `json:"gameSessionInfo"`
		} `json:"detail"`
	}
	require.NoError(t, json.Unmarshal(raw, &out))
	require.Len(t, out.Detail.GameSessionInfo.Players, 1)
	assert.Equal(t, "p1", out.Detail.GameSessionInfo.Players[0].PlayerID)
}

func TestTranslator_MarshalProducesStableJSON(t *testing.T) {
	tk := makeTicket(t)
	events := tk.PullEvents()
	tr := notification.NewTranslator(idgen.NewSequence("e-"), notification.EnvelopeSettings{Region: "us-east-1", AccountID: "000000000000"}, lookupFor(tk))
	raw, err := tr.Marshal(events[0])
	require.NoError(t, err)
	var out map[string]any
	require.NoError(t, json.Unmarshal(raw, &out))
	assert.Equal(t, "aws.gamelift", out["source"])
}
