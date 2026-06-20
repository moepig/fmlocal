// Package notification adapts the EventPublisher port. It translates domain
// matchmaking events into AWS EventBridge envelopes and delivers them either
// to an HTTP endpoint (mimicking an SNS HTTP subscription) or to an SQS queue
// (mimicking EventBridge targeting SQS).
package notification

import (
	"encoding/json"
	"fmt"

	"github.com/moepig/fmlocal/internal/app/ports"
	mm "github.com/moepig/fmlocal/internal/domain/matchmaking"
)

// estimatedWaitNotAvailable is the sentinel AWS uses for MatchmakingSearching
// when no wait-time estimate is available.
const estimatedWaitNotAvailable = "NOT_AVAILABLE"

// ISO8601Millis is the timestamp layout AWS uses in matchmaking events:
// ISO 8601 with millisecond precision (e.g. 2017-08-09T19:59:09.159Z).
const ISO8601Millis = "2006-01-02T15:04:05.000Z07:00"

// EventBridgeEnvelope mirrors the on-the-wire shape AWS emits.
type EventBridgeEnvelope struct {
	Version    string   `json:"version"`
	ID         string   `json:"id"`
	DetailType string   `json:"detail-type"`
	Source     string   `json:"source"`
	Account    string   `json:"account"`
	Time       string   `json:"time"`
	Region     string   `json:"region"`
	Resources  []string `json:"resources"`
	Detail     Detail   `json:"detail"`
}

// Detail is the per-event payload under EventBridge's "detail" field.
type Detail struct {
	Type                 string           `json:"type"`
	Tickets              []TicketDetail   `json:"tickets,omitempty"`
	MatchID              string           `json:"matchId,omitempty"`
	AcceptanceRequired   *bool            `json:"acceptanceRequired,omitempty"`
	AcceptanceTimeout    *int             `json:"acceptanceTimeout,omitempty"`
	Acceptance           string           `json:"acceptance,omitempty"`
	RuleEvaluationMetric []RuleEvalMetric `json:"ruleEvaluationMetrics,omitempty"`
	// EstimatedWaitMillis is an integer (in seconds, despite the AWS field
	// name) or the string "NOT_AVAILABLE" when no estimate exists.
	EstimatedWaitMillis any `json:"estimatedWaitMillis,omitempty"`
	GameSessionInfo      *GameSessionInfo `json:"gameSessionInfo,omitempty"`
	CustomEventData      string           `json:"customEventData,omitempty"`
	Reason               string           `json:"reason,omitempty"`
	Message              string           `json:"message,omitempty"`
}

type TicketDetail struct {
	TicketID  string         `json:"ticketId"`
	StartTime string         `json:"startTime,omitempty"`
	Players   []PlayerDetail `json:"players"`
}

type PlayerDetail struct {
	PlayerID        string `json:"playerId"`
	Accepted        *bool  `json:"accepted,omitempty"`
	PlayerSessionID string `json:"playerSessionId,omitempty"`
	Team            string `json:"team,omitempty"`
}

type RuleEvalMetric struct {
	RuleName    string `json:"ruleName"`
	PassedCount int    `json:"passedCount"`
	FailedCount int    `json:"failedCount"`
}

// GameSessionInfo mirrors AWS's per-event gameSessionInfo block. Every
// matchmaking event carries it with the flattened player roster. In STANDALONE
// mode no game session is created, so the connection fields (ipAddress, port,
// gameSessionArn, ...) are legitimately absent; only players (and, on
// MatchmakingSucceeded, matchId) are populated.
type GameSessionInfo struct {
	IPAddress      string         `json:"ipAddress,omitempty"`
	Port           int            `json:"port,omitempty"`
	GameSessionARN string         `json:"gameSessionArn,omitempty"`
	MatchID        string         `json:"matchId,omitempty"`
	Players        []PlayerDetail `json:"players"`
}

// EnvelopeSettings captures the regional/account metadata needed to render
// an EventBridge envelope. It is typically derived from configuration.
type EnvelopeSettings struct {
	Region    string
	AccountID string
}

// Translator converts a domain event into an EventBridge envelope, resolving
// the per-event detail using the TicketLookup callback.
type Translator struct {
	ids      ports.IDGenerator
	settings EnvelopeSettings
	lookup   TicketLookup
}

// TicketLookup returns ticket details for enriching event payloads. When a
// ticket cannot be found (e.g., already pruned) it should return ok=false.
type TicketLookup func(id mm.TicketID) (TicketDetail, bool)

func NewTranslator(ids ports.IDGenerator, settings EnvelopeSettings, lookup TicketLookup) *Translator {
	return &Translator{ids: ids, settings: settings, lookup: lookup}
}

// Render produces the JSON envelope for e.
func (t *Translator) Render(e mm.Event) (EventBridgeEnvelope, error) {
	detail, err := t.buildDetail(e)
	if err != nil {
		return EventBridgeEnvelope{}, err
	}
	return EventBridgeEnvelope{
		Version:    "0",
		ID:         t.ids.NewID(),
		DetailType: "GameLift Matchmaking Event",
		Source:     "aws.gamelift",
		Account:    t.settings.AccountID,
		Time:       e.OccurredAt().UTC().Format(ISO8601Millis),
		Region:     t.settings.Region,
		Resources: []string{fmt.Sprintf(
			"arn:aws:gamelift:%s:%s:matchmakingconfiguration/%s",
			t.settings.Region, t.settings.AccountID, e.ConfigurationName(),
		)},
		Detail: detail,
	}, nil
}

// Marshal returns the rendered envelope as JSON bytes.
func (t *Translator) Marshal(e mm.Event) ([]byte, error) {
	env, err := t.Render(e)
	if err != nil {
		return nil, err
	}
	return json.Marshal(env)
}

func (t *Translator) buildDetail(e mm.Event) (Detail, error) {
	d := Detail{Type: e.EventName()}
	switch ev := e.(type) {
	case mm.EventTicketSearchingStarted:
		d.Tickets = t.lookupTickets(ev.TicketID())
		// AWS always includes estimatedWaitMillis on MatchmakingSearching.
		// fmlocal does not compute wait estimates, so report NOT_AVAILABLE.
		d.EstimatedWaitMillis = estimatedWaitNotAvailable
	case mm.EventTicketAssignedToProposal:
		d.MatchID = string(ev.MatchID())
		d.Tickets = t.lookupTickets(ev.TicketIDs()...)
		d.RuleEvaluationMetric = toWireRuleMetrics(ev.RuleMetrics())
		required := ev.AcceptanceRequired()
		d.AcceptanceRequired = &required
		// AWS reports acceptanceTimeout in seconds; only meaningful when
		// acceptance is required.
		if required {
			secs := int(ev.AcceptanceTimeout().Seconds())
			d.AcceptanceTimeout = &secs
		}
	case mm.EventPlayerAcceptanceRecorded:
		d.MatchID = string(ev.MatchID())
		td := t.lookupTickets(ev.TicketIDs()...)
		acted := map[string]bool{}
		for _, pid := range ev.PlayerIDs() {
			acted[string(pid)] = true
		}
		accepted := ev.Accepted()
		for i := range td {
			for j := range td[i].Players {
				if acted[td[i].Players[j].PlayerID] {
					v := accepted
					td[i].Players[j].Accepted = &v
				}
			}
		}
		d.Tickets = td
	case mm.EventAcceptanceCompleted:
		d.MatchID = string(ev.MatchID())
		d.Acceptance = string(ev.Outcome())
		d.Tickets = t.lookupTickets(ev.TicketIDs()...)
	case mm.EventMatchmakingSucceeded:
		d.MatchID = string(ev.MatchID())
		d.Tickets = t.lookupTickets(ev.TicketIDs()...)
	case mm.EventMatchmakingFailed:
		d.MatchID = string(ev.MatchID())
		d.Reason = ev.Reason()
		d.Message = ev.Message()
		d.Tickets = t.lookupTickets(ev.TicketID())
	case mm.EventMatchmakingTimedOut:
		d.MatchID = string(ev.MatchID())
		d.Reason = ev.Reason()
		d.Message = ev.Message()
		d.Tickets = t.lookupTickets(ev.TicketID())
		d.RuleEvaluationMetric = toWireRuleMetrics(ev.RuleMetrics())
	case mm.EventMatchmakingCancelled:
		d.MatchID = string(ev.MatchID())
		d.Reason = "Cancelled"
		d.Message = "Cancelled by request."
		d.Tickets = t.lookupTickets(ev.TicketID())
		d.RuleEvaluationMetric = toWireRuleMetrics(ev.RuleMetrics())
	default:
		return Detail{}, fmt.Errorf("notification: unknown event %T", e)
	}
	// AWS attaches gameSessionInfo to every matchmaking event, with a player
	// roster flattened across the event's tickets (carrying the same team /
	// accepted fields). MatchmakingSucceeded additionally identifies the match.
	gsi := &GameSessionInfo{Players: flattenTicketPlayers(d.Tickets)}
	if _, ok := e.(mm.EventMatchmakingSucceeded); ok {
		gsi.MatchID = d.MatchID
	}
	d.GameSessionInfo = gsi
	return d, nil
}

// flattenTicketPlayers concatenates every ticket's players into a single
// roster for gameSessionInfo. The PlayerDetail values already carry the per
// event team/accepted fields resolved during ticket lookup.
func flattenTicketPlayers(tickets []TicketDetail) []PlayerDetail {
	out := make([]PlayerDetail, 0, len(tickets))
	for _, t := range tickets {
		out = append(out, t.Players...)
	}
	return out
}

// toWireRuleMetrics converts domain rule-evaluation metrics into the wire shape
// AWS emits under detail.ruleEvaluationMetrics.
func toWireRuleMetrics(src []mm.RuleEvaluationMetric) []RuleEvalMetric {
	if len(src) == 0 {
		return nil
	}
	out := make([]RuleEvalMetric, len(src))
	for i, m := range src {
		out[i] = RuleEvalMetric{RuleName: m.RuleName, PassedCount: m.PassedCount, FailedCount: m.FailedCount}
	}
	return out
}

func (t *Translator) lookupTickets(ids ...mm.TicketID) []TicketDetail {
	if t.lookup == nil {
		return nil
	}
	out := make([]TicketDetail, 0, len(ids))
	for _, id := range ids {
		if td, ok := t.lookup(id); ok {
			out = append(out, td)
		}
	}
	return out
}
