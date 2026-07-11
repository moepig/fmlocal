package matchmaking_test

import (
	"context"
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

// verbatimRS is a rule set exactly as exported from AWS: it carries none of
// flexi's non-standard extension fields (acceptanceRequired,
// acceptanceTimeoutSeconds, requestTimeoutSeconds) — on AWS those live on the
// MatchmakingConfiguration.
const verbatimRS = `{
  "name": "1v1",
  "ruleLanguageVersion": "1.0",
  "teams": [
    {"name": "red",  "minPlayers": 1, "maxPlayers": 1},
    {"name": "blue", "minPlayers": 1, "maxPlayers": 1}
  ]
}`

// conflictingRS declares engine settings that contradict the configuration
// used in the tests below; the configuration must win.
const conflictingRS = `{
  "name": "1v1",
  "ruleLanguageVersion": "1.0",
  "teams": [
    {"name": "red",  "minPlayers": 1, "maxPlayers": 1},
    {"name": "blue", "minPlayers": 1, "maxPlayers": 1}
  ],
  "acceptanceRequired": true,
  "acceptanceTimeoutSeconds": 30,
  "requestTimeoutSeconds": 1
}`

func setupBuilt(t *testing.T, ruleset string, cfg mm.Configuration) *harness {
	t.Helper()
	clk := sysclock.NewFake(time.Date(2026, 4, 18, 10, 0, 0, 0, time.UTC))
	rs := mm.RuleSet{Name: cfg.RuleSetName, Body: []byte(ruleset)}
	engine, err := appmm.BuildEngine(cfg, rs, flexi.WithClock(clk))
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

func startTwo(t *testing.T, h *harness) {
	t.Helper()
	ctx := context.Background()
	for _, id := range []mm.TicketID{"t1", "t2"} {
		_, err := h.svc.StartMatchmaking(ctx, appmm.StartMatchmakingCommand{
			ConfigurationName: "c1",
			TicketID:          id,
			Players:           []flexi.Player{{ID: string(id)}},
		})
		require.NoError(t, err)
	}
}

// A verbatim AWS rule set plus a configuration requiring acceptance must run
// the acceptance phase: the configuration is the source of truth, not flexi's
// rule set extension fields.
func TestBuildEngine_ConfigurationEnablesAcceptance(t *testing.T) {
	h := setupBuilt(t, verbatimRS, mm.Configuration{
		Name: "c1", RuleSetName: "rs1", FlexMatchMode: mm.FlexMatchModeStandalone,
		RequestTimeout:     60 * time.Second,
		AcceptanceRequired: true,
		AcceptanceTimeout:  10 * time.Second,
	})
	startTwo(t, h)
	require.NoError(t, h.svc.Tick(context.Background(), "c1"))
	t1, err := h.svc.GetTicket("t1")
	require.NoError(t, err)
	assert.Equal(t, mm.StatusRequiresAcceptance, t1.Status())
	assert.Contains(t, h.pub.Names(), "PotentialMatchCreated")
}

// A rule set carrying acceptanceRequired: true must not force an acceptance
// phase when the configuration says acceptance is not required.
func TestBuildEngine_ConfigurationDisablesAcceptance(t *testing.T) {
	h := setupBuilt(t, conflictingRS, mm.Configuration{
		Name: "c1", RuleSetName: "rs1", FlexMatchMode: mm.FlexMatchModeStandalone,
		RequestTimeout:     60 * time.Second,
		AcceptanceRequired: false,
	})
	startTwo(t, h)
	require.NoError(t, h.svc.Tick(context.Background(), "c1"))
	t1, err := h.svc.GetTicket("t1")
	require.NoError(t, err)
	assert.Equal(t, mm.StatusCompleted, t1.Status())
	assert.Contains(t, h.pub.Names(), "MatchmakingSucceeded")
}

// A rule set carrying requestTimeoutSeconds must not expire tickets when the
// configuration sets no request timeout.
func TestBuildEngine_ConfigurationControlsRequestTimeout(t *testing.T) {
	h := setupBuilt(t, conflictingRS, mm.Configuration{
		Name: "c1", RuleSetName: "rs1", FlexMatchMode: mm.FlexMatchModeStandalone,
		RequestTimeout: 0, // no timeout; the rule set says 1s
	})
	ctx := context.Background()
	_, err := h.svc.StartMatchmaking(ctx, appmm.StartMatchmakingCommand{
		ConfigurationName: "c1",
		TicketID:          "solo",
		Players:           []flexi.Player{{ID: "p1"}},
	})
	require.NoError(t, err)
	h.clock.Advance(5 * time.Second)
	require.NoError(t, h.svc.Tick(ctx, "c1"))
	tk, err := h.svc.GetTicket("solo")
	require.NoError(t, err)
	assert.True(t, tk.Status().IsActive(), "ticket must not expire, got %s", tk.Status())
}
