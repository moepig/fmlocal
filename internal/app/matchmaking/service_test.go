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

type harness struct {
	svc   *appmm.Service
	pub   *capturePublisher
	clock *sysclock.Fake
}

func setup(t *testing.T, ruleset string, acceptance bool) *harness {
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
	engine, err := flexi.New(rs.Body, flexi.WithClock(clk))
	require.NoError(t, err)
	resolver := appmm.NewStaticEngineResolver()
	resolver.Register(cfg.Name, engine)
	pub := &capturePublisher{}
	svc := &appmm.Service{
		Engines:    resolver,
		Publishers: map[mm.ConfigurationName]ports.EventPublisher{cfg.Name: pub},
		Clock:      clk,
		IDs:        idgen.NewSequence("ticket-"),
		MatchIDs:   idgen.NewSequence("match-"),
	}
	svc.LoadConfigurations([]mm.Configuration{cfg})
	svc.LoadRuleSets([]mm.RuleSet{rs})
	return &harness{svc: svc, pub: pub, clock: clk}
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
	assert.Equal(t, mm.StatusCancelled, t1.Status())
	assert.Contains(t, h.pub.Names(), "MatchmakingFailed")
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
	assert.Equal(t, mm.StatusTimedOut, t1.Status())
	assert.Contains(t, h.pub.Names(), "MatchmakingTimedOut")
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
