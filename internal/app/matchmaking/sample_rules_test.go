package matchmaking_test

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/moepig/flexi"
	appmm "github.com/moepig/fmlocal/internal/app/matchmaking"
	mm "github.com/moepig/fmlocal/internal/domain/matchmaking"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func sampleRuleSet(t *testing.T, name string) string {
	t.Helper()
	local, err := os.ReadFile(filepath.Join("..", "..", "..", "testdata", "rulesets", name+".json"))
	require.NoError(t, err)
	deployed, err := os.ReadFile(filepath.Join("..", "..", "..", "deploy", "local", "rulesets", name+".json"))
	require.NoError(t, err)
	assert.True(t, bytes.Equal(local, deployed), "testdata and deployed rule sets differ: %s", name)
	var doc map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(local, &doc))
	assert.NotContains(t, doc, "acceptanceRequired")
	assert.NotContains(t, doc, "acceptanceTimeoutSeconds")
	return string(local)
}

func skillPlayer(id string, skill float64) flexi.Player {
	return flexi.Player{ID: id, Attributes: flexi.Attributes{"skill": flexi.Number(skill)}}
}

func TestSampleRuleSet_OneVersusOne(t *testing.T) {
	h := setup(t, sampleRuleSet(t, "1v1"), false)
	ctx := context.Background()
	_, err := h.svc.StartMatchmaking(ctx, appmm.StartMatchmakingCommand{
		ConfigurationName: "c1", TicketID: "missing", Players: []flexi.Player{{ID: "p0"}},
	})
	require.ErrorIs(t, err, mm.ErrInvalidRequest)

	for _, player := range []flexi.Player{skillPlayer("p1", 20), skillPlayer("p2", 70)} {
		_, err := h.svc.StartMatchmaking(ctx, appmm.StartMatchmakingCommand{
			ConfigurationName: "c1", TicketID: mm.TicketID(player.ID), Players: []flexi.Player{player},
		})
		require.NoError(t, err)
	}
	require.NoError(t, h.svc.Tick(ctx, "c1"))
	for _, id := range []mm.TicketID{"p1", "p2"} {
		ticket, err := h.svc.GetTicket(id)
		require.NoError(t, err)
		assert.Equal(t, mm.StatusCompleted, ticket.Status())
	}
}

func TestSampleRuleSet_OneVersusOneRejectsSkillGap(t *testing.T) {
	h := setup(t, sampleRuleSet(t, "1v1"), false)
	ctx := context.Background()
	for _, player := range []flexi.Player{skillPlayer("p1", 20), skillPlayer("p2", 71)} {
		_, err := h.svc.StartMatchmaking(ctx, appmm.StartMatchmakingCommand{
			ConfigurationName: "c1", TicketID: mm.TicketID(player.ID), Players: []flexi.Player{player},
		})
		require.NoError(t, err)
	}
	require.NoError(t, h.svc.Tick(ctx, "c1"))
	for _, id := range []mm.TicketID{"p1", "p2"} {
		ticket, err := h.svc.GetTicket(id)
		require.NoError(t, err)
		assert.Equal(t, mm.StatusQueued, ticket.Status())
	}
}

func TestSampleRuleSet_OneVersusOneAcceptance(t *testing.T) {
	h := setup(t, sampleRuleSet(t, "1v1-accept"), true)
	ctx := context.Background()
	for _, player := range []flexi.Player{skillPlayer("p1", 20), skillPlayer("p2", 80)} {
		_, err := h.svc.StartMatchmaking(ctx, appmm.StartMatchmakingCommand{
			ConfigurationName: "c1", TicketID: mm.TicketID(player.ID), Players: []flexi.Player{player},
		})
		require.NoError(t, err)
	}
	require.NoError(t, h.svc.Tick(ctx, "c1"))
	for _, id := range []mm.TicketID{"p1", "p2"} {
		ticket, err := h.svc.GetTicket(id)
		require.NoError(t, err)
		assert.Equal(t, mm.StatusRequiresAcceptance, ticket.Status())
		require.NoError(t, h.svc.AcceptMatch(ctx, appmm.AcceptMatchCommand{
			TicketID: id, PlayerIDs: []mm.PlayerID{mm.PlayerID(id)}, Accepted: true,
		}))
	}
	require.NoError(t, h.svc.Tick(ctx, "c1"))
	for _, id := range []mm.TicketID{"p1", "p2"} {
		ticket, err := h.svc.GetTicket(id)
		require.NoError(t, err)
		assert.Equal(t, mm.StatusCompleted, ticket.Status())
	}
}

func TestSampleRuleSet_Backfill(t *testing.T) {
	h := setup(t, sampleRuleSet(t, "2v2-backfill"), false)
	ctx := context.Background()
	seated := []flexi.Player{skillPlayer("p1", 40), skillPlayer("p2", 50), skillPlayer("p3", 60)}
	seated[0].Team, seated[1].Team, seated[2].Team = "red", "red", "blue"
	_, err := h.svc.StartMatchBackfill(ctx, appmm.StartMatchBackfillCommand{
		ConfigurationName: "c1", TicketID: "missing", Players: []flexi.Player{
			seated[0], seated[1], {ID: "p0", Team: "blue"},
		},
	})
	require.ErrorIs(t, err, mm.ErrInvalidRequest)

	backfill, err := h.svc.StartMatchBackfill(ctx, appmm.StartMatchBackfillCommand{
		ConfigurationName: "c1", TicketID: "bf", Players: seated,
	})
	require.NoError(t, err)
	newcomer, err := h.svc.StartMatchmaking(ctx, appmm.StartMatchmakingCommand{
		ConfigurationName: "c1", TicketID: "new", Players: []flexi.Player{skillPlayer("p4", 70)},
	})
	require.NoError(t, err)
	require.NoError(t, h.svc.Tick(ctx, "c1"))
	assert.Equal(t, mm.StatusCompleted, backfill.Status())
	assert.Equal(t, mm.StatusCompleted, newcomer.Status())
	assert.Equal(t, "blue", newcomer.PlayerTeam("p4"))
}
