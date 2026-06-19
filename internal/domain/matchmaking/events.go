package matchmaking

import "time"

// AcceptanceOutcome is the terminal decision for a proposal under acceptance.
type AcceptanceOutcome string

const (
	AcceptanceAccepted AcceptanceOutcome = "Accepted"
	AcceptanceRejected AcceptanceOutcome = "Rejected"
	AcceptanceTimedOut AcceptanceOutcome = "TimedOut"
)

// RuleEvaluationMetric is the per-rule pass/fail tally FlexMatch reports in the
// ruleEvaluationMetrics array of PotentialMatchCreated, MatchmakingTimedOut and
// MatchmakingCancelled events. The values originate from the matchmaking engine.
type RuleEvaluationMetric struct {
	RuleName    string
	PassedCount int
	FailedCount int
}

// Event is the base interface for matchmaking domain events. Adapters
// translate events into whatever wire format their transport requires (for
// fmlocal: AWS EventBridge envelopes).
type Event interface {
	isMatchmakingEvent()
	EventName() string
	ConfigurationName() ConfigurationName
	OccurredAt() time.Time
}

// Domain event names. These are the values EventName() returns and the
// strings published consumers see in the EventBridge envelope's detail.type.
const (
	EventNameMatchmakingSearching  = "MatchmakingSearching"
	EventNamePotentialMatchCreated = "PotentialMatchCreated"
	EventNameAcceptMatch           = "AcceptMatch"
	EventNameAcceptMatchCompleted  = "AcceptMatchCompleted"
	EventNameMatchmakingSucceeded  = "MatchmakingSucceeded"
	EventNameMatchmakingFailed     = "MatchmakingFailed"
	EventNameMatchmakingTimedOut   = "MatchmakingTimedOut"
	EventNameMatchmakingCancelled  = "MatchmakingCancelled"
)

// KnownEventNames is the exhaustive set of domain event names fmlocal emits.
// Configuration loaders can validate against it.
var KnownEventNames = []string{
	EventNameMatchmakingSearching,
	EventNamePotentialMatchCreated,
	EventNameAcceptMatch,
	EventNameAcceptMatchCompleted,
	EventNameMatchmakingSucceeded,
	EventNameMatchmakingFailed,
	EventNameMatchmakingTimedOut,
	EventNameMatchmakingCancelled,
}

type baseEvent struct {
	configName ConfigurationName
	occurredAt time.Time
}

func (e baseEvent) isMatchmakingEvent()                   {}
func (e baseEvent) ConfigurationName() ConfigurationName { return e.configName }
func (e baseEvent) OccurredAt() time.Time                 { return e.occurredAt }

// EventTicketSearchingStarted fires when a new ticket enters matchmaking.
type EventTicketSearchingStarted struct {
	ticketID   TicketID
	configName ConfigurationName
	occurredAt time.Time
}

func (e EventTicketSearchingStarted) isMatchmakingEvent()                    {}
func (e EventTicketSearchingStarted) EventName() string                       { return "MatchmakingSearching" }
func (e EventTicketSearchingStarted) ConfigurationName() ConfigurationName    { return e.configName }
func (e EventTicketSearchingStarted) OccurredAt() time.Time                   { return e.occurredAt }
func (e EventTicketSearchingStarted) TicketID() TicketID                      { return e.ticketID }

// Match-grouping events.
//
// PotentialMatchCreated, AcceptMatch, AcceptMatchCompleted and
// MatchmakingSucceeded describe a whole match, not a single ticket. AWS emits
// each one once per match with every participating ticket in the payload (see
// the FlexMatch event docs). They are therefore constructed by the application
// layer — which knows the full ticket roster of a match — rather than recorded
// per-ticket by the Ticket aggregate. Each carries the complete set of ticket
// IDs via TicketIDs().

// EventTicketAssignedToProposal maps to PotentialMatchCreated: flexi has formed
// a candidate match from the given tickets.
type EventTicketAssignedToProposal struct {
	configName  ConfigurationName
	matchID     MatchID
	ticketIDs   []TicketID
	ruleMetrics []RuleEvaluationMetric
	occurredAt  time.Time
}

// NewPotentialMatchCreated builds a match-level PotentialMatchCreated event.
func NewPotentialMatchCreated(cfg ConfigurationName, matchID MatchID, ticketIDs []TicketID, ruleMetrics []RuleEvaluationMetric, now time.Time) EventTicketAssignedToProposal {
	return EventTicketAssignedToProposal{configName: cfg, matchID: matchID, ticketIDs: ticketIDs, ruleMetrics: ruleMetrics, occurredAt: now}
}

func (e EventTicketAssignedToProposal) isMatchmakingEvent()                 {}
func (e EventTicketAssignedToProposal) EventName() string                    { return "PotentialMatchCreated" }
func (e EventTicketAssignedToProposal) ConfigurationName() ConfigurationName { return e.configName }
func (e EventTicketAssignedToProposal) OccurredAt() time.Time                { return e.occurredAt }
func (e EventTicketAssignedToProposal) TicketIDs() []TicketID                { return e.ticketIDs }
func (e EventTicketAssignedToProposal) MatchID() MatchID                     { return e.matchID }
func (e EventTicketAssignedToProposal) RuleMetrics() []RuleEvaluationMetric  { return e.ruleMetrics }

// EventPlayerAcceptanceRecorded maps to AcceptMatch: one or more players in the
// match responded Accept or Reject. The event carries every ticket in the match
// and the players whose acceptance this notification reflects.
type EventPlayerAcceptanceRecorded struct {
	configName ConfigurationName
	matchID    MatchID
	ticketIDs  []TicketID
	playerIDs  []PlayerID
	accepted   bool
	occurredAt time.Time
}

// NewAcceptMatch builds a match-level AcceptMatch event for the players that
// just responded with the given acceptance value.
func NewAcceptMatch(cfg ConfigurationName, matchID MatchID, ticketIDs []TicketID, playerIDs []PlayerID, accepted bool, now time.Time) EventPlayerAcceptanceRecorded {
	return EventPlayerAcceptanceRecorded{configName: cfg, matchID: matchID, ticketIDs: ticketIDs, playerIDs: playerIDs, accepted: accepted, occurredAt: now}
}

func (e EventPlayerAcceptanceRecorded) isMatchmakingEvent()                 {}
func (e EventPlayerAcceptanceRecorded) EventName() string                    { return "AcceptMatch" }
func (e EventPlayerAcceptanceRecorded) ConfigurationName() ConfigurationName { return e.configName }
func (e EventPlayerAcceptanceRecorded) OccurredAt() time.Time                { return e.occurredAt }
func (e EventPlayerAcceptanceRecorded) TicketIDs() []TicketID                { return e.ticketIDs }
func (e EventPlayerAcceptanceRecorded) MatchID() MatchID                     { return e.matchID }
func (e EventPlayerAcceptanceRecorded) PlayerIDs() []PlayerID                { return e.playerIDs }
func (e EventPlayerAcceptanceRecorded) Accepted() bool                       { return e.accepted }

// EventAcceptanceCompleted maps to AcceptMatchCompleted: the match's acceptance
// phase terminally settled (all accepted, rejected, or timed out).
type EventAcceptanceCompleted struct {
	configName ConfigurationName
	matchID    MatchID
	ticketIDs  []TicketID
	outcome    AcceptanceOutcome
	occurredAt time.Time
}

// NewAcceptMatchCompleted builds a match-level AcceptMatchCompleted event.
func NewAcceptMatchCompleted(cfg ConfigurationName, matchID MatchID, ticketIDs []TicketID, outcome AcceptanceOutcome, now time.Time) EventAcceptanceCompleted {
	return EventAcceptanceCompleted{configName: cfg, matchID: matchID, ticketIDs: ticketIDs, outcome: outcome, occurredAt: now}
}

func (e EventAcceptanceCompleted) isMatchmakingEvent()                 {}
func (e EventAcceptanceCompleted) EventName() string                    { return "AcceptMatchCompleted" }
func (e EventAcceptanceCompleted) ConfigurationName() ConfigurationName { return e.configName }
func (e EventAcceptanceCompleted) OccurredAt() time.Time                { return e.occurredAt }
func (e EventAcceptanceCompleted) TicketIDs() []TicketID                { return e.ticketIDs }
func (e EventAcceptanceCompleted) MatchID() MatchID                     { return e.matchID }
func (e EventAcceptanceCompleted) Outcome() AcceptanceOutcome           { return e.outcome }

// EventMatchmakingSucceeded maps to MatchmakingSucceeded: the match completed
// successfully. Carries every ticket in the match.
type EventMatchmakingSucceeded struct {
	configName ConfigurationName
	matchID    MatchID
	ticketIDs  []TicketID
	occurredAt time.Time
}

// NewMatchmakingSucceeded builds a match-level MatchmakingSucceeded event.
func NewMatchmakingSucceeded(cfg ConfigurationName, matchID MatchID, ticketIDs []TicketID, now time.Time) EventMatchmakingSucceeded {
	return EventMatchmakingSucceeded{configName: cfg, matchID: matchID, ticketIDs: ticketIDs, occurredAt: now}
}

func (e EventMatchmakingSucceeded) isMatchmakingEvent()                 {}
func (e EventMatchmakingSucceeded) EventName() string                    { return "MatchmakingSucceeded" }
func (e EventMatchmakingSucceeded) ConfigurationName() ConfigurationName { return e.configName }
func (e EventMatchmakingSucceeded) OccurredAt() time.Time                { return e.occurredAt }
func (e EventMatchmakingSucceeded) TicketIDs() []TicketID                { return e.ticketIDs }
func (e EventMatchmakingSucceeded) MatchID() MatchID                     { return e.matchID }

// EventMatchmakingFailed fires when a proposal is rejected.
type EventMatchmakingFailed struct {
	ticketID   TicketID
	configName ConfigurationName
	matchID    MatchID
	reason     string
	message    string
	occurredAt time.Time
}

func (e EventMatchmakingFailed) isMatchmakingEvent()                 {}
func (e EventMatchmakingFailed) EventName() string                    { return "MatchmakingFailed" }
func (e EventMatchmakingFailed) ConfigurationName() ConfigurationName { return e.configName }
func (e EventMatchmakingFailed) OccurredAt() time.Time                { return e.occurredAt }
func (e EventMatchmakingFailed) TicketID() TicketID                   { return e.ticketID }
func (e EventMatchmakingFailed) MatchID() MatchID                     { return e.matchID }
func (e EventMatchmakingFailed) Reason() string                       { return e.reason }
func (e EventMatchmakingFailed) Message() string                      { return e.message }

// EventMatchmakingTimedOut fires on request or acceptance timeout.
type EventMatchmakingTimedOut struct {
	ticketID    TicketID
	configName  ConfigurationName
	matchID     MatchID
	reason      string
	message     string
	ruleMetrics []RuleEvaluationMetric
	occurredAt  time.Time
}

func (e EventMatchmakingTimedOut) isMatchmakingEvent()                  {}
func (e EventMatchmakingTimedOut) EventName() string                    { return "MatchmakingTimedOut" }
func (e EventMatchmakingTimedOut) ConfigurationName() ConfigurationName { return e.configName }
func (e EventMatchmakingTimedOut) OccurredAt() time.Time                { return e.occurredAt }
func (e EventMatchmakingTimedOut) TicketID() TicketID                   { return e.ticketID }
func (e EventMatchmakingTimedOut) MatchID() MatchID                     { return e.matchID }
func (e EventMatchmakingTimedOut) Reason() string                       { return e.reason }
func (e EventMatchmakingTimedOut) Message() string                      { return e.message }
func (e EventMatchmakingTimedOut) RuleMetrics() []RuleEvaluationMetric  { return e.ruleMetrics }

// EventMatchmakingCancelled fires when the user stops matchmaking.
type EventMatchmakingCancelled struct {
	ticketID    TicketID
	configName  ConfigurationName
	matchID     MatchID
	ruleMetrics []RuleEvaluationMetric
	occurredAt  time.Time
}

func (e EventMatchmakingCancelled) isMatchmakingEvent()                  {}
func (e EventMatchmakingCancelled) EventName() string                    { return "MatchmakingCancelled" }
func (e EventMatchmakingCancelled) ConfigurationName() ConfigurationName { return e.configName }
func (e EventMatchmakingCancelled) OccurredAt() time.Time                { return e.occurredAt }
func (e EventMatchmakingCancelled) TicketID() TicketID                   { return e.ticketID }
func (e EventMatchmakingCancelled) MatchID() MatchID                     { return e.matchID }
func (e EventMatchmakingCancelled) RuleMetrics() []RuleEvaluationMetric  { return e.ruleMetrics }
