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

// baseEvent carries the fields and Event-interface boilerplate shared by every
// event type; each embeds it and adds only its own payload and EventName.
type baseEvent struct {
	configName ConfigurationName
	occurredAt time.Time
}

func (e baseEvent) isMatchmakingEvent()                  {}
func (e baseEvent) ConfigurationName() ConfigurationName { return e.configName }
func (e baseEvent) OccurredAt() time.Time                { return e.occurredAt }

// EventTicketSearchingStarted fires when a new ticket enters matchmaking.
type EventTicketSearchingStarted struct {
	baseEvent
	ticketID TicketID
}

func (e EventTicketSearchingStarted) EventName() string  { return EventNameMatchmakingSearching }
func (e EventTicketSearchingStarted) TicketID() TicketID { return e.ticketID }

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
	baseEvent
	matchID            MatchID
	ticketIDs          []TicketID
	ruleMetrics        []RuleEvaluationMetric
	acceptanceRequired bool
	acceptanceTimeout  time.Duration
}

// NewPotentialMatchCreated builds a match-level PotentialMatchCreated event.
// acceptanceRequired/acceptanceTimeout come from the matchmaking configuration
// so consumers learn whether (and how long) they must accept the proposal.
func NewPotentialMatchCreated(cfg ConfigurationName, matchID MatchID, ticketIDs []TicketID, ruleMetrics []RuleEvaluationMetric, acceptanceRequired bool, acceptanceTimeout time.Duration, now time.Time) EventTicketAssignedToProposal {
	return EventTicketAssignedToProposal{
		baseEvent:          baseEvent{configName: cfg, occurredAt: now},
		matchID:            matchID,
		ticketIDs:          ticketIDs,
		ruleMetrics:        ruleMetrics,
		acceptanceRequired: acceptanceRequired,
		acceptanceTimeout:  acceptanceTimeout,
	}
}

func (e EventTicketAssignedToProposal) EventName() string                   { return EventNamePotentialMatchCreated }
func (e EventTicketAssignedToProposal) TicketIDs() []TicketID               { return e.ticketIDs }
func (e EventTicketAssignedToProposal) MatchID() MatchID                    { return e.matchID }
func (e EventTicketAssignedToProposal) RuleMetrics() []RuleEvaluationMetric { return e.ruleMetrics }
func (e EventTicketAssignedToProposal) AcceptanceRequired() bool            { return e.acceptanceRequired }
func (e EventTicketAssignedToProposal) AcceptanceTimeout() time.Duration    { return e.acceptanceTimeout }

// EventPlayerAcceptanceRecorded maps to AcceptMatch: one or more players in the
// match responded Accept or Reject. The event carries every ticket in the match
// and the cumulative acceptance status of all players who have responded so far
// (playerID -> accepted), matching AWS, which reports each player's current
// acceptance state on every AcceptMatch event.
type EventPlayerAcceptanceRecorded struct {
	baseEvent
	matchID     MatchID
	ticketIDs   []TicketID
	acceptances map[PlayerID]bool
}

// NewAcceptMatch builds a match-level AcceptMatch event. acceptances holds the
// cumulative per-player decisions recorded so far; players absent from it have
// not yet responded and are rendered without an accepted flag.
func NewAcceptMatch(cfg ConfigurationName, matchID MatchID, ticketIDs []TicketID, acceptances map[PlayerID]bool, now time.Time) EventPlayerAcceptanceRecorded {
	return EventPlayerAcceptanceRecorded{baseEvent: baseEvent{configName: cfg, occurredAt: now}, matchID: matchID, ticketIDs: ticketIDs, acceptances: acceptances}
}

func (e EventPlayerAcceptanceRecorded) EventName() string              { return EventNameAcceptMatch }
func (e EventPlayerAcceptanceRecorded) TicketIDs() []TicketID          { return e.ticketIDs }
func (e EventPlayerAcceptanceRecorded) MatchID() MatchID               { return e.matchID }
func (e EventPlayerAcceptanceRecorded) Acceptances() map[PlayerID]bool { return e.acceptances }

// EventAcceptanceCompleted maps to AcceptMatchCompleted: the match's acceptance
// phase terminally settled (all accepted, rejected, or timed out).
type EventAcceptanceCompleted struct {
	baseEvent
	matchID   MatchID
	ticketIDs []TicketID
	outcome   AcceptanceOutcome
}

// NewAcceptMatchCompleted builds a match-level AcceptMatchCompleted event.
func NewAcceptMatchCompleted(cfg ConfigurationName, matchID MatchID, ticketIDs []TicketID, outcome AcceptanceOutcome, now time.Time) EventAcceptanceCompleted {
	return EventAcceptanceCompleted{baseEvent: baseEvent{configName: cfg, occurredAt: now}, matchID: matchID, ticketIDs: ticketIDs, outcome: outcome}
}

func (e EventAcceptanceCompleted) EventName() string          { return EventNameAcceptMatchCompleted }
func (e EventAcceptanceCompleted) TicketIDs() []TicketID      { return e.ticketIDs }
func (e EventAcceptanceCompleted) MatchID() MatchID           { return e.matchID }
func (e EventAcceptanceCompleted) Outcome() AcceptanceOutcome { return e.outcome }

// EventMatchmakingSucceeded maps to MatchmakingSucceeded: the match completed
// successfully. Carries every ticket in the match.
type EventMatchmakingSucceeded struct {
	baseEvent
	matchID   MatchID
	ticketIDs []TicketID
}

// NewMatchmakingSucceeded builds a match-level MatchmakingSucceeded event.
func NewMatchmakingSucceeded(cfg ConfigurationName, matchID MatchID, ticketIDs []TicketID, now time.Time) EventMatchmakingSucceeded {
	return EventMatchmakingSucceeded{baseEvent: baseEvent{configName: cfg, occurredAt: now}, matchID: matchID, ticketIDs: ticketIDs}
}

func (e EventMatchmakingSucceeded) EventName() string     { return EventNameMatchmakingSucceeded }
func (e EventMatchmakingSucceeded) TicketIDs() []TicketID { return e.ticketIDs }
func (e EventMatchmakingSucceeded) MatchID() MatchID      { return e.matchID }

// EventMatchmakingFailed fires when a proposal is rejected.
type EventMatchmakingFailed struct {
	baseEvent
	ticketID    TicketID
	matchID     MatchID
	reason      string
	message     string
	ruleMetrics []RuleEvaluationMetric
}

func (e EventMatchmakingFailed) EventName() string                   { return EventNameMatchmakingFailed }
func (e EventMatchmakingFailed) TicketID() TicketID                  { return e.ticketID }
func (e EventMatchmakingFailed) MatchID() MatchID                    { return e.matchID }
func (e EventMatchmakingFailed) Reason() string                      { return e.reason }
func (e EventMatchmakingFailed) Message() string                     { return e.message }
func (e EventMatchmakingFailed) RuleMetrics() []RuleEvaluationMetric { return e.ruleMetrics }

// EventMatchmakingTimedOut fires on request or acceptance timeout.
type EventMatchmakingTimedOut struct {
	baseEvent
	ticketID    TicketID
	matchID     MatchID
	reason      string
	message     string
	ruleMetrics []RuleEvaluationMetric
}

func (e EventMatchmakingTimedOut) EventName() string                   { return EventNameMatchmakingTimedOut }
func (e EventMatchmakingTimedOut) TicketID() TicketID                  { return e.ticketID }
func (e EventMatchmakingTimedOut) MatchID() MatchID                    { return e.matchID }
func (e EventMatchmakingTimedOut) Reason() string                      { return e.reason }
func (e EventMatchmakingTimedOut) Message() string                     { return e.message }
func (e EventMatchmakingTimedOut) RuleMetrics() []RuleEvaluationMetric { return e.ruleMetrics }

// EventMatchmakingCancelled fires when the user stops matchmaking.
type EventMatchmakingCancelled struct {
	baseEvent
	ticketID    TicketID
	matchID     MatchID
	ruleMetrics []RuleEvaluationMetric
}

func (e EventMatchmakingCancelled) EventName() string                   { return EventNameMatchmakingCancelled }
func (e EventMatchmakingCancelled) TicketID() TicketID                  { return e.ticketID }
func (e EventMatchmakingCancelled) MatchID() MatchID                    { return e.matchID }
func (e EventMatchmakingCancelled) RuleMetrics() []RuleEvaluationMetric { return e.ruleMetrics }
