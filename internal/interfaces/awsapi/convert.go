package awsapi

import (
	"time"

	"github.com/moepig/flexi"
	mm "github.com/moepig/fmlocal/internal/domain/matchmaking"
)

func playersFromDTO(in []Player) []flexi.Player {
	out := make([]flexi.Player, 0, len(in))
	for _, p := range in {
		out = append(out, flexi.Player{
			ID:         p.PlayerID,
			Attributes: attributesFromDTO(p.PlayerAttributes),
			Latencies:  p.LatencyInMs,
		})
	}
	return out
}

func attributesFromDTO(in map[string]AttributeValue) flexi.Attributes {
	if len(in) == 0 {
		return nil
	}
	out := make(flexi.Attributes, len(in))
	for k, v := range in {
		switch {
		case v.S != nil:
			out[k] = flexi.String(*v.S)
		case v.N != nil:
			out[k] = flexi.Number(*v.N)
		case len(v.SL) > 0:
			out[k] = flexi.StringList(v.SL...)
		case len(v.SDM) > 0:
			out[k] = flexi.StringNumberMap(v.SDM)
		}
	}
	return out
}

func playersToDTO(in []flexi.Player) []Player {
	out := make([]Player, 0, len(in))
	for _, p := range in {
		out = append(out, Player{
			PlayerID:         p.ID,
			LatencyInMs:      p.Latencies,
			PlayerAttributes: attributesToDTO(p.Attributes),
		})
	}
	return out
}

func attributesToDTO(in flexi.Attributes) map[string]AttributeValue {
	if len(in) == 0 {
		return nil
	}
	out := make(map[string]AttributeValue, len(in))
	for k, v := range in {
		switch v.Kind {
		case flexi.AttrString:
			s := v.S
			out[k] = AttributeValue{S: &s}
		case flexi.AttrNumber:
			n := v.N
			out[k] = AttributeValue{N: &n}
		case flexi.AttrStringList:
			out[k] = AttributeValue{SL: append([]string(nil), v.SL...)}
		case flexi.AttrStringNumberMap:
			cp := make(map[string]float64, len(v.SDM))
			for kk, vv := range v.SDM {
				cp[kk] = vv
			}
			out[k] = AttributeValue{SDM: cp}
		}
	}
	return out
}

func ticketToDTO(t *mm.Ticket) MatchmakingTicket {
	dto := MatchmakingTicket{
		TicketID:          string(t.ID()),
		ConfigurationName: string(t.ConfigurationName()),
		ConfigurationARN:  t.ConfigurationARN(),
		Status:            string(t.Status()),
		StatusReason:      t.StatusReason(),
		StatusMessage:     t.StatusMessage(),
		Players:           playersToDTO(t.Players()),
	}
	if !t.StartTime().IsZero() {
		dto.StartTime = unixSeconds(t.StartTime())
	}
	if !t.EndTime().IsZero() {
		dto.EndTime = unixSeconds(t.EndTime())
	}
	if w := t.EstimatedWait(); w != nil {
		v := int(w.Seconds())
		dto.EstimatedWaitTime = &v
	}
	return dto
}

func configurationToDTO(cfg mm.Configuration) MatchmakingConfiguration {
	var notification string
	if len(cfg.NotificationTargetIDs) > 0 {
		notification = cfg.NotificationTargetIDs[0]
	}
	flex := string(cfg.FlexMatchMode)
	if flex == "" {
		flex = string(mm.FlexMatchModeStandalone)
	}
	return MatchmakingConfiguration{
		Name:                     string(cfg.Name),
		ConfigurationARN:         cfg.ARN,
		RuleSetName:              string(cfg.RuleSetName),
		RuleSetARN:               cfg.RuleSetARN,
		RequestTimeoutSeconds:    int(cfg.RequestTimeout.Seconds()),
		AcceptanceRequired:       cfg.AcceptanceRequired,
		AcceptanceTimeoutSeconds: int(cfg.AcceptanceTimeout.Seconds()),
		BackfillMode:             string(cfg.BackfillMode),
		FlexMatchMode:            flex,
		NotificationTarget:       notification,
	}
}

func ruleSetToDTO(rs mm.RuleSet) MatchmakingRuleSet {
	return MatchmakingRuleSet{
		RuleSetName: string(rs.Name),
		RuleSetARN:  rs.ARN,
		RuleSetBody: string(rs.Body),
	}
}

func unixSeconds(t time.Time) float64 {
	return float64(t.UnixNano()) / 1e9
}
