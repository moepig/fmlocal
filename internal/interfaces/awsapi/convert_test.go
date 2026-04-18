package awsapi

import (
	"testing"

	"github.com/moepig/flexi"
	mm "github.com/moepig/fmlocal/internal/domain/matchmaking"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestPlayersFromDTO_AllAttributeKinds(t *testing.T) {
	n := 42.0
	s := "hello"
	in := []Player{
		{
			PlayerID:    "p1",
			LatencyInMs: map[string]int{"us-east-1": 50},
			PlayerAttributes: map[string]AttributeValue{
				"num":  {N: &n},
				"str":  {S: &s},
				"list": {SL: []string{"a", "b"}},
				"map":  {SDM: map[string]float64{"x": 1.0}},
			},
		},
	}
	out := playersFromDTO(in)
	require.Len(t, out, 1)
	p := out[0]
	assert.Equal(t, "p1", p.ID)
	assert.Equal(t, 50, p.Latencies["us-east-1"])
	assert.Equal(t, flexi.AttrNumber, p.Attributes["num"].Kind)
	assert.Equal(t, 42.0, p.Attributes["num"].N)
	assert.Equal(t, flexi.AttrString, p.Attributes["str"].Kind)
	assert.Equal(t, "hello", p.Attributes["str"].S)
	assert.Equal(t, flexi.AttrStringList, p.Attributes["list"].Kind)
	assert.Equal(t, []string{"a", "b"}, p.Attributes["list"].SL)
	assert.Equal(t, flexi.AttrStringNumberMap, p.Attributes["map"].Kind)
	assert.Equal(t, 1.0, p.Attributes["map"].SDM["x"])
}

func TestAttributesToDTO_AllKinds(t *testing.T) {
	attrs := flexi.Attributes{
		"n":   flexi.Number(3.14),
		"s":   flexi.String("hi"),
		"sl":  flexi.StringList("x", "y"),
		"sdm": flexi.StringNumberMap(map[string]float64{"k": 9.0}),
	}
	dtos := attributesToDTO(attrs)
	require.NotNil(t, dtos["n"].N)
	assert.InEpsilon(t, 3.14, *dtos["n"].N, 1e-9)
	require.NotNil(t, dtos["s"].S)
	assert.Equal(t, "hi", *dtos["s"].S)
	assert.Equal(t, []string{"x", "y"}, dtos["sl"].SL)
	assert.Equal(t, 9.0, dtos["sdm"].SDM["k"])
}

func TestConfigurationToDTO(t *testing.T) {
	cfg := mm.Configuration{
		Name:                     "c1",
		ARN:                      "arn:aws:gamelift:us-east-1:000000000000:matchmakingconfiguration/c1",
		RuleSetName:              "rs1",
		RuleSetARN:               "arn:aws:gamelift:us-east-1:000000000000:matchmakingruleset/rs1",
		RequestTimeout:           60e9, // 60s in nanoseconds
		AcceptanceRequired:       true,
		AcceptanceTimeout:        10e9,
		BackfillMode:             mm.BackfillManual,
		FlexMatchMode:            mm.FlexMatchModeStandalone,
		NotificationTargetIDs:    []string{"sink1", "sink2"},
	}
	dto := configurationToDTO(cfg)
	assert.Equal(t, "c1", dto.Name)
	assert.Equal(t, cfg.ARN, dto.ConfigurationARN)
	assert.Equal(t, "rs1", dto.RuleSetName)
	assert.Equal(t, 60, dto.RequestTimeoutSeconds)
	assert.True(t, dto.AcceptanceRequired)
	assert.Equal(t, 10, dto.AcceptanceTimeoutSeconds)
	assert.Equal(t, "MANUAL", dto.BackfillMode)
	assert.Equal(t, "STANDALONE", dto.FlexMatchMode)
	// NotificationTarget holds only the first ID (mirrors AWS's single-target field).
	assert.Equal(t, "sink1", dto.NotificationTarget)
}

func TestTranslateDomainError_Coverage(t *testing.T) {
	cases := []struct {
		err      error
		wantType string
	}{
		{mm.ErrTicketNotFound, "NotFoundException"},
		{mm.ErrConfigurationNotFound, "NotFoundException"},
		{mm.ErrRuleSetNotFound, "NotFoundException"},
		{mm.ErrTicketAlreadyExists, "InvalidRequestException"},
		{mm.ErrPlayerNotInTicket, "InvalidRequestException"},
		{mm.ErrProposalNotFound, "InvalidRequestException"},
		{mm.ErrInvalidTransition, "InvalidRequestException"},
		{mm.ErrInvalidRuleSet, "InvalidRequestException"},
		{mm.ErrBackfillUnsupported, "UnsupportedOperationException"},
	}
	for _, tc := range cases {
		apiErr := translateDomainError(tc.err)
		require.NotNil(t, apiErr, "%v", tc.err)
		assert.Equal(t, tc.wantType, apiErr.TypeName, "error: %v", tc.err)
	}
	// Unknown error maps to InternalServiceException.
	unknown := &struct{ error }{nil}
	_ = unknown
	apiErr := translateDomainError(assert.AnError)
	assert.Equal(t, "InternalServiceException", apiErr.TypeName)
}
