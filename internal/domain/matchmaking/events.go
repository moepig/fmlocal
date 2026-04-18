package matchmaking

import "time"

// AcceptanceOutcome is the terminal decision for a proposal under acceptance.
type AcceptanceOutcome string

const (
	AcceptanceAccepted AcceptanceOutcome = "Accepted"
	AcceptanceRejected AcceptanceOutcome = "Rejected"
	AcceptanceTimedOut AcceptanceOutcome = "TimedOut"
)

// Event is the base interface for matchmaking domain events. Adapters
// translate events into whatever wire format their transport requires (for
// fmlocal: AWS EventBridge envelopes).
type Event interface {
	isMatchmakingEvent()
	EventName() string
	ConfigurationName() ConfigurationName
	OccurredAt() time.Time
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

// EventTicketAssignedToProposal fires when flexi forms a candidate match
// requiring acceptance and this ticket is part of it.
type EventTicketAssignedToProposal struct {
	ticketID   TicketID
	configName ConfigurationName
	matchID    MatchID
	occurredAt time.Time
}

func (e EventTicketAssignedToProposal) isMatchmakingEvent()                 {}
func (e EventTicketAssignedToProposal) EventName() string                    { return "PotentialMatchCreated" }
func (e EventTicketAssignedToProposal) ConfigurationName() ConfigurationName { return e.configName }
func (e EventTicketAssignedToProposal) OccurredAt() time.Time                { return e.occurredAt }
func (e EventTicketAssignedToProposal) TicketID() TicketID                   { return e.ticketID }
func (e EventTicketAssignedToProposal) MatchID() MatchID                     { return e.matchID }

// EventPlayerAcceptanceRecorded fires when a player responds Accept or Reject.
type EventPlayerAcceptanceRecorded struct {
	ticketID   TicketID
	configName ConfigurationName
	matchID    MatchID
	playerID   PlayerID
	accepted   bool
	occurredAt time.Time
}

func (e EventPlayerAcceptanceRecorded) isMatchmakingEvent()                 {}
func (e EventPlayerAcceptanceRecorded) EventName() string                    { return "AcceptMatch" }
func (e EventPlayerAcceptanceRecorded) ConfigurationName() ConfigurationName { return e.configName }
func (e EventPlayerAcceptanceRecorded) OccurredAt() time.Time                { return e.occurredAt }
func (e EventPlayerAcceptanceRecorded) TicketID() TicketID                   { return e.ticketID }
func (e EventPlayerAcceptanceRecorded) MatchID() MatchID                     { return e.matchID }
func (e EventPlayerAcceptanceRecorded) PlayerID() PlayerID                   { return e.playerID }
func (e EventPlayerAcceptanceRecorded) Accepted() bool                       { return e.accepted }

// EventAcceptanceCompleted fires when a proposal terminally settles.
type EventAcceptanceCompleted struct {
	ticketID   TicketID
	configName ConfigurationName
	matchID    MatchID
	outcome    AcceptanceOutcome
	occurredAt time.Time
}

func (e EventAcceptanceCompleted) isMatchmakingEvent()                 {}
func (e EventAcceptanceCompleted) EventName() string                    { return "AcceptMatchCompleted" }
func (e EventAcceptanceCompleted) ConfigurationName() ConfigurationName { return e.configName }
func (e EventAcceptanceCompleted) OccurredAt() time.Time                { return e.occurredAt }
func (e EventAcceptanceCompleted) TicketID() TicketID                   { return e.ticketID }
func (e EventAcceptanceCompleted) MatchID() MatchID                     { return e.matchID }
func (e EventAcceptanceCompleted) Outcome() AcceptanceOutcome           { return e.outcome }

// EventMatchmakingSucceeded fires on terminal success.
type EventMatchmakingSucceeded struct {
	ticketID   TicketID
	configName ConfigurationName
	matchID    MatchID
	occurredAt time.Time
}

func (e EventMatchmakingSucceeded) isMatchmakingEvent()                 {}
func (e EventMatchmakingSucceeded) EventName() string                    { return "MatchmakingSucceeded" }
func (e EventMatchmakingSucceeded) ConfigurationName() ConfigurationName { return e.configName }
func (e EventMatchmakingSucceeded) OccurredAt() time.Time                { return e.occurredAt }
func (e EventMatchmakingSucceeded) TicketID() TicketID                   { return e.ticketID }
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
	ticketID   TicketID
	configName ConfigurationName
	matchID    MatchID
	reason     string
	message    string
	occurredAt time.Time
}

func (e EventMatchmakingTimedOut) isMatchmakingEvent()                 {}
func (e EventMatchmakingTimedOut) EventName() string                    { return "MatchmakingTimedOut" }
func (e EventMatchmakingTimedOut) ConfigurationName() ConfigurationName { return e.configName }
func (e EventMatchmakingTimedOut) OccurredAt() time.Time                { return e.occurredAt }
func (e EventMatchmakingTimedOut) TicketID() TicketID                   { return e.ticketID }
func (e EventMatchmakingTimedOut) MatchID() MatchID                     { return e.matchID }
func (e EventMatchmakingTimedOut) Reason() string                       { return e.reason }
func (e EventMatchmakingTimedOut) Message() string                      { return e.message }

// EventMatchmakingCancelled fires when the user stops matchmaking.
type EventMatchmakingCancelled struct {
	ticketID   TicketID
	configName ConfigurationName
	matchID    MatchID
	occurredAt time.Time
}

func (e EventMatchmakingCancelled) isMatchmakingEvent()                 {}
func (e EventMatchmakingCancelled) EventName() string                    { return "MatchmakingCancelled" }
func (e EventMatchmakingCancelled) ConfigurationName() ConfigurationName { return e.configName }
func (e EventMatchmakingCancelled) OccurredAt() time.Time                { return e.occurredAt }
func (e EventMatchmakingCancelled) TicketID() TicketID                   { return e.ticketID }
func (e EventMatchmakingCancelled) MatchID() MatchID                     { return e.matchID }
