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
			StartTime: tk.StartTime().UTC().Format(time.RFC3339),
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
	assert.Equal(t, "MatchmakingSearching", env.Detail.Type)
	require.Len(t, env.Detail.Tickets, 1)
	assert.Equal(t, "t1", env.Detail.Tickets[0].TicketID)
	assert.Equal(t, []string{"arn:aws:gamelift:us-east-1:000000000000:matchmakingconfiguration/cfg"}, env.Resources)
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
				_ = tk.PullEvents()
				_ = tk.AssignToProposal("m-1", now)
				return tk.PullEvents()[0]
			},
			wantTyp: "PotentialMatchCreated",
		},
		{
			name: "AcceptMatch",
			setup: func(tk *mm.Ticket) mm.Event {
				_ = tk.PullEvents()
				_ = tk.AssignToProposal("m-1", now)
				_ = tk.PullEvents()
				_ = tk.RecordPlayerAcceptance("p1", true, now)
				return tk.PullEvents()[0]
			},
			wantTyp: "AcceptMatch",
		},
		{
			name: "AcceptMatchCompleted",
			setup: func(tk *mm.Ticket) mm.Event {
				_ = tk.PullEvents()
				_ = tk.AssignToProposal("m-1", now)
				_ = tk.PullEvents()
				tk.AcceptanceCompleted(mm.AcceptanceAccepted, now)
				return tk.PullEvents()[0]
			},
			wantTyp: "AcceptMatchCompleted",
		},
		{
			name: "MatchmakingSucceeded",
			setup: func(tk *mm.Ticket) mm.Event {
				_ = tk.PullEvents()
				_ = tk.AssignToProposal("m-1", now)
				_ = tk.PullEvents()
				_ = tk.MoveToPlacing("m-1", now)
				_ = tk.Complete(now)
				evs := tk.PullEvents()
				// Last event is MatchmakingSucceeded
				return evs[len(evs)-1]
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
