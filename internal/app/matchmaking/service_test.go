package matchmaking_test

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/moepig/flexi"
	"github.com/moepig/fmlocal/internal/app/defaults/idgen"
	"github.com/moepig/fmlocal/internal/app/defaults/sysclock"
	appmm "github.com/moepig/fmlocal/internal/app/matchmaking"
	"github.com/moepig/fmlocal/internal/app/ports"
	mm "github.com/moepig/fmlocal/internal/domain/matchmaking"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const skillRS = `{
  "name": "1v1",
  "ruleLanguageVersion": "1.0",
  "playerAttributes": [{"name": "skill", "type": "number"}],
  "teams": [
    {"name": "red",  "minPlayers": 1, "maxPlayers": 1},
    {"name": "blue", "minPlayers": 1, "maxPlayers": 1}
  ]
}`

const skillRSAccept = `{
  "name": "1v1-accept",
  "ruleLanguageVersion": "1.0",
  "playerAttributes": [{"name": "skill", "type": "number"}],
  "teams": [
    {"name": "red",  "minPlayers": 1, "maxPlayers": 1},
    {"name": "blue", "minPlayers": 1, "maxPlayers": 1}
  ],
  "acceptanceRequired": true,
  "acceptanceTimeoutSeconds": 30
}`

type capturePublisher struct {
	mu     sync.Mutex
	events []mm.Event
}

func (c *capturePublisher) Publish(_ context.Context, e mm.Event) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.events = append(c.events, e)
	return nil
}

func (c *capturePublisher) Names() []string {
	c.mu.Lock()
	defer c.mu.Unlock()
	out := make([]string, 0, len(c.events))
	for _, e := range c.events {
		out = append(out, e.EventName())
	}
	return out
}

func countName(names []string, want string) int {
	n := 0
	for _, name := range names {
		if name == want {
			n++
		}
	}
	return n
}

type harness struct {
	svc   *appmm.Service
	pub   *capturePublisher
	clock *sysclock.Fake
}

func setup(t *testing.T, ruleset string, acceptance bool) *harness {
	t.Helper()
	pub := &capturePublisher{}
	h := setupWithPublisher(t, ruleset, acceptance, pub)
	h.pub = pub
	return h
}

func setupWithPublisher(t *testing.T, ruleset string, acceptance bool, pub ports.EventPublisher) *harness {
	t.Helper()
	clk := sysclock.NewFake(time.Date(2026, 4, 18, 10, 0, 0, 0, time.UTC))
	cfg := mm.Configuration{
		Name:               "c1",
		RuleSetName:        "rs1",
		FlexMatchMode:      mm.FlexMatchModeStandalone,
		RequestTimeout:     60 * time.Second,
		AcceptanceRequired: acceptance,
		AcceptanceTimeout:  10 * time.Second,
	}
	rs := mm.RuleSet{Name: "rs1", Body: []byte(ruleset)}
	engine, err := appmm.BuildEngine(cfg, rs, flexi.WithClock(clk))
	require.NoError(t, err)
	resolver := appmm.NewStaticEngineResolver()
	resolver.Register(cfg.Name, engine)
	svc := &appmm.Service{
		Engines:    resolver,
		Publishers: map[mm.ConfigurationName]ports.EventPublisher{cfg.Name: pub},
		Clock:      clk,
		IDs:        idgen.NewSequence("ticket-"),
		MatchIDs:   idgen.NewSequence("match-"),
	}
	svc.LoadConfigurations([]mm.Configuration{cfg})
	svc.LoadRuleSets([]mm.RuleSet{rs})
	return &harness{svc: svc, clock: clk}
}

func TestService_StartEmitsSearching(t *testing.T) {
	h := setup(t, skillRS, false)
	ticket, err := h.svc.StartMatchmaking(context.Background(), appmm.StartMatchmakingCommand{
		ConfigurationName: "c1",
		Players:           []flexi.Player{{ID: "p1"}},
	})
	require.NoError(t, err)
	assert.Equal(t, mm.StatusQueued, ticket.Status())
	assert.Equal(t, []string{"MatchmakingSearching"}, h.pub.Names())
}

func TestService_TickCompletesMatch(t *testing.T) {
	h := setup(t, skillRS, false)
	ctx := context.Background()
	for _, id := range []mm.TicketID{"t1", "t2"} {
		_, err := h.svc.StartMatchmaking(ctx, appmm.StartMatchmakingCommand{
			ConfigurationName: "c1",
			TicketID:          id,
			Players:           []flexi.Player{{ID: string(id)}},
		})
		require.NoError(t, err)
	}
	require.NoError(t, h.svc.Tick(ctx, "c1"))
	t1, _ := h.svc.GetTicket("t1")
	t2, _ := h.svc.GetTicket("t2")
	assert.Equal(t, mm.StatusCompleted, t1.Status())
	assert.Equal(t, mm.StatusCompleted, t2.Status())
	assert.Contains(t, h.pub.Names(), "MatchmakingSucceeded")
}

func TestService_AcceptanceFlow(t *testing.T) {
	h := setup(t, skillRSAccept, true)
	ctx := context.Background()
	for _, id := range []mm.TicketID{"t1", "t2"} {
		_, err := h.svc.StartMatchmaking(ctx, appmm.StartMatchmakingCommand{
			ConfigurationName: "c1",
			TicketID:          id,
			Players:           []flexi.Player{{ID: string(id)}},
		})
		require.NoError(t, err)
	}
	require.NoError(t, h.svc.Tick(ctx, "c1"))
	t1, _ := h.svc.GetTicket("t1")
	assert.Equal(t, mm.StatusRequiresAcceptance, t1.Status())

	for _, id := range []mm.TicketID{"t1", "t2"} {
		require.NoError(t, h.svc.AcceptMatch(ctx, appmm.AcceptMatchCommand{
			TicketID:  id,
			PlayerIDs: []mm.PlayerID{mm.PlayerID(id)},
			Accepted:  true,
		}))
	}
	require.NoError(t, h.svc.Tick(ctx, "c1"))
	t1, _ = h.svc.GetTicket("t1")
	assert.Equal(t, mm.StatusCompleted, t1.Status())
	names := h.pub.Names()
	assert.Contains(t, names, "PotentialMatchCreated")
	assert.Contains(t, names, "AcceptMatchCompleted")
	assert.Contains(t, names, "MatchmakingSucceeded")
}

func TestService_RejectFailsMatch(t *testing.T) {
	h := setup(t, skillRSAccept, true)
	ctx := context.Background()
	for _, id := range []mm.TicketID{"t1", "t2"} {
		_, err := h.svc.StartMatchmaking(ctx, appmm.StartMatchmakingCommand{
			ConfigurationName: "c1",
			TicketID:          id,
			Players:           []flexi.Player{{ID: string(id)}},
		})
		require.NoError(t, err)
	}
	require.NoError(t, h.svc.Tick(ctx, "c1"))
	require.NoError(t, h.svc.AcceptMatch(ctx, appmm.AcceptMatchCommand{
		TicketID:  "t1",
		PlayerIDs: []mm.PlayerID{"t1"},
		Accepted:  false,
	}))
	require.NoError(t, h.svc.Tick(ctx, "c1"))
	t1, _ := h.svc.GetTicket("t1")
	// Neither player accepted, so both tickets are cancelled (t1 because its
	// player rejected; t2 because it never accepted). AWS emits
	// MatchmakingCancelled — not MatchmakingFailed — for an acceptance failure,
	// and AcceptMatchCompleted carries acceptance=Rejected.
	assert.Equal(t, mm.StatusCancelled, t1.Status())
	assert.Contains(t, h.pub.Names(), "MatchmakingCancelled")
	assert.NotContains(t, h.pub.Names(), "MatchmakingFailed")
}

// TestService_RejectReQueuesAcceptingTicket covers the AWS behavior that a
// ticket whose players all accepted is returned to the pool (re-emitting
// MatchmakingSearching) when a sibling rejects, while only the rejecting ticket
// is cancelled.
func TestService_RejectReQueuesAcceptingTicket(t *testing.T) {
	h := setup(t, skillRSAccept, true)
	ctx := context.Background()
	for _, id := range []mm.TicketID{"t1", "t2"} {
		_, err := h.svc.StartMatchmaking(ctx, appmm.StartMatchmakingCommand{
			ConfigurationName: "c1",
			TicketID:          id,
			Players:           []flexi.Player{{ID: string(id)}},
		})
		require.NoError(t, err)
	}
	require.NoError(t, h.svc.Tick(ctx, "c1"))
	// t1 accepts, t2 rejects.
	require.NoError(t, h.svc.AcceptMatch(ctx, appmm.AcceptMatchCommand{
		TicketID: "t1", PlayerIDs: []mm.PlayerID{"t1"}, Accepted: true,
	}))
	require.NoError(t, h.svc.AcceptMatch(ctx, appmm.AcceptMatchCommand{
		TicketID: "t2", PlayerIDs: []mm.PlayerID{"t2"}, Accepted: false,
	}))
	require.NoError(t, h.svc.Tick(ctx, "c1"))

	t1, _ := h.svc.GetTicket("t1")
	t2, _ := h.svc.GetTicket("t2")
	// t1 accepted → returned to the pool; t2 rejected → cancelled.
	assert.Equal(t, mm.StatusSearching, t1.Status())
	assert.Equal(t, mm.StatusCancelled, t2.Status())
	// The re-queued ticket carries the engine's status reason, which
	// DescribeMatchmaking surfaces as MatchmakingTicket.StatusReason.
	assert.Equal(t, "ACCEPTANCE_FAILED", t1.StatusReason())
	// Two initial MatchmakingSearching (t1, t2) plus t1's re-queue re-emit = 3;
	// t2's cancel emits MatchmakingCancelled; AcceptMatchCompleted reports the
	// rejection once.
	names := h.pub.Names()
	assert.Equal(t, 3, countName(names, "MatchmakingSearching"))
	assert.Contains(t, names, "AcceptMatchCompleted")
	assert.Contains(t, names, "MatchmakingCancelled")
	assert.NotContains(t, names, "MatchmakingFailed")
}

func TestService_AcceptanceTimeout(t *testing.T) {
	h := setup(t, skillRSAccept, true)
	ctx := context.Background()
	for _, id := range []mm.TicketID{"t1", "t2"} {
		_, err := h.svc.StartMatchmaking(ctx, appmm.StartMatchmakingCommand{
			ConfigurationName: "c1",
			TicketID:          id,
			Players:           []flexi.Player{{ID: string(id)}},
		})
		require.NoError(t, err)
	}
	require.NoError(t, h.svc.Tick(ctx, "c1"))
	h.clock.Advance(31 * time.Second)
	require.NoError(t, h.svc.Tick(ctx, "c1"))
	t1, _ := h.svc.GetTicket("t1")
	// No player accepted before the acceptance timeout elapsed: AWS reserves
	// TIMED_OUT for the request-level timeout and cancels acceptance failures,
	// so the tickets end CANCELLED and AcceptMatchCompleted reports TimedOut.
	assert.Equal(t, mm.StatusCancelled, t1.Status())
	assert.Contains(t, h.pub.Names(), "MatchmakingCancelled")
	assert.NotContains(t, h.pub.Names(), "MatchmakingTimedOut")
}

func TestService_StopMatchmaking(t *testing.T) {
	h := setup(t, skillRS, false)
	ctx := context.Background()
	_, err := h.svc.StartMatchmaking(ctx, appmm.StartMatchmakingCommand{
		ConfigurationName: "c1",
		TicketID:          "solo",
		Players:           []flexi.Player{{ID: "p1"}},
	})
	require.NoError(t, err)
	require.NoError(t, h.svc.StopMatchmaking(ctx, appmm.StopMatchmakingCommand{TicketID: "solo"}))
	require.NoError(t, h.svc.Tick(ctx, "c1"))
	tk, _ := h.svc.GetTicket("solo")
	assert.Equal(t, mm.StatusCancelled, tk.Status())
	assert.Contains(t, h.pub.Names(), "MatchmakingCancelled")
}

func TestService_RequestTimeout(t *testing.T) {
	h := setup(t, skillRS, false)
	h.svc.LoadConfigurations([]mm.Configuration{{
		Name: "c1", FlexMatchMode: mm.FlexMatchModeStandalone, RequestTimeout: 5 * time.Second,
	}})
	ctx := context.Background()
	_, err := h.svc.StartMatchmaking(ctx, appmm.StartMatchmakingCommand{
		ConfigurationName: "c1",
		TicketID:          "solo",
		Players:           []flexi.Player{{ID: "p1"}},
	})
	require.NoError(t, err)
	h.clock.Advance(6 * time.Second)
	require.NoError(t, h.svc.Tick(ctx, "c1"))
	tk, _ := h.svc.GetTicket("solo")
	assert.Equal(t, mm.StatusTimedOut, tk.Status())
	assert.Contains(t, h.pub.Names(), "MatchmakingTimedOut")
}

func TestService_TerminalTicketsAreEvictedAfterRetention(t *testing.T) {
	h := setup(t, skillRS, false)
	h.svc.TicketRetention = time.Minute
	ctx := context.Background()
	_, err := h.svc.StartMatchmaking(ctx, appmm.StartMatchmakingCommand{
		ConfigurationName: "c1",
		TicketID:          "solo",
		Players:           []flexi.Player{{ID: "p1"}},
	})
	require.NoError(t, err)
	require.NoError(t, h.svc.StopMatchmaking(ctx, appmm.StopMatchmakingCommand{TicketID: "solo"}))
	require.NoError(t, h.svc.Tick(ctx, "c1"))
	tk, err := h.svc.GetTicket("solo")
	require.NoError(t, err)
	require.Equal(t, mm.StatusCancelled, tk.Status())

	// Still described while within the retention window.
	h.clock.Advance(30 * time.Second)
	require.NoError(t, h.svc.Tick(ctx, "c1"))
	_, err = h.svc.GetTicket("solo")
	require.NoError(t, err)

	// Gone once the retention window has elapsed, on both sides: the engine
	// retains a spent ticket's status and rule metrics until evicted, so an
	// expired ticket that only fmlocal forgot would still leak there.
	h.clock.Advance(31 * time.Second)
	require.NoError(t, h.svc.Tick(ctx, "c1"))
	_, err = h.svc.GetTicket("solo")
	assert.ErrorIs(t, err, mm.ErrTicketNotFound)
	assert.Empty(t, h.svc.TicketsByConfiguration("c1"))
	engine, err := h.svc.Engines.EngineFor("c1")
	require.NoError(t, err)
	_, err = engine.Status("solo")
	assert.ErrorIs(t, err, flexi.ErrUnknownTicket)
}

func TestService_DescribeConfigurations(t *testing.T) {
	h := setup(t, skillRS, false)
	ctx := context.Background()
	all, err := h.svc.DescribeConfigurations(ctx, appmm.DescribeConfigurationsQuery{})
	require.NoError(t, err)
	require.Len(t, all, 1)
	assert.Equal(t, mm.ConfigurationName("c1"), all[0].Name)

	filtered, err := h.svc.DescribeConfigurations(ctx, appmm.DescribeConfigurationsQuery{
		Names: []mm.ConfigurationName{"c1"},
	})
	require.NoError(t, err)
	require.Len(t, filtered, 1)

	none, err := h.svc.DescribeConfigurations(ctx, appmm.DescribeConfigurationsQuery{
		Names: []mm.ConfigurationName{"ghost"},
	})
	require.NoError(t, err)
	assert.Len(t, none, 0)

	byRS, err := h.svc.DescribeConfigurations(ctx, appmm.DescribeConfigurationsQuery{
		RuleSetName: "rs1",
	})
	require.NoError(t, err)
	require.Len(t, byRS, 1)
}

func TestService_DescribeRuleSets(t *testing.T) {
	h := setup(t, skillRS, false)
	ctx := context.Background()
	all, err := h.svc.DescribeRuleSets(ctx, appmm.DescribeRuleSetsQuery{})
	require.NoError(t, err)
	require.Len(t, all, 1)
	assert.Equal(t, mm.RuleSetName("rs1"), all[0].Name)

	filtered, err := h.svc.DescribeRuleSets(ctx, appmm.DescribeRuleSetsQuery{Names: []mm.RuleSetName{"rs1"}})
	require.NoError(t, err)
	require.Len(t, filtered, 1)

	none, err := h.svc.DescribeRuleSets(ctx, appmm.DescribeRuleSetsQuery{Names: []mm.RuleSetName{"ghost"}})
	require.NoError(t, err)
	assert.Len(t, none, 0)
}

func TestService_ValidateRuleSet(t *testing.T) {
	h := setup(t, skillRS, false)
	ctx := context.Background()
	require.NoError(t, h.svc.ValidateRuleSet(ctx, appmm.ValidateRuleSetCommand{Body: []byte(skillRS)}))
	require.Error(t, h.svc.ValidateRuleSet(ctx, appmm.ValidateRuleSetCommand{Body: []byte(`{}`)}))
}
