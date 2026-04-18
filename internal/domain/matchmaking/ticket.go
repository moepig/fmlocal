package matchmaking

import (
	"fmt"
	"time"

	"github.com/moepig/flexi"
)

// clonePlayers makes a shallow copy of a flexi.Player slice. flexi.Player
// fields (Attributes, Latencies) are maps/slices that are safe to share as
// read-only, which is sufficient here since Ticket never mutates them.
func clonePlayers(src []flexi.Player) []flexi.Player {
	if src == nil {
		return nil
	}
	out := make([]flexi.Player, len(src))
	copy(out, src)
	return out
}

// Ticket is the aggregate root of the matchmaking bounded context. It owns
// its status transitions: external callers never mutate fields directly but
// invoke intent-revealing methods, each of which enforces the invariant that
// the transition is legal from the current state.
//
// Ticket also records the domain events that result from those transitions.
// Callers (application-layer use cases) pull events after each mutation and
// hand them to an EventPublisher port.
type Ticket struct {
	id                TicketID
	configurationName ConfigurationName
	configurationARN  string
	players           []Player
	status            TicketStatus
	statusReason      string
	statusMessage     string
	startTime         time.Time
	endTime           time.Time
	matchID           MatchID
	cancelByAPI       bool
	searchingEmitted  bool
	estimatedWait     *time.Duration

	events []Event
}

// NewTicket constructs a freshly-created ticket in StatusQueued. It emits a
// SearchingStarted event so the client sees matchmaking activity immediately.
func NewTicket(id TicketID, cfg Configuration, players []Player, now time.Time) (*Ticket, error) {
	if id == "" {
		return nil, fmt.Errorf("matchmaking: ticket id is required")
	}
	if len(players) == 0 {
		return nil, fmt.Errorf("matchmaking: ticket must have at least one player")
	}
	t := &Ticket{
		id:                id,
		configurationName: cfg.Name,
		configurationARN:  cfg.ARN,
		players:           clonePlayers(players),
		status:            StatusQueued,
		startTime:         now,
		searchingEmitted:  true,
	}
	t.recordEvent(EventTicketSearchingStarted{
		ticketID:   id,
		configName: cfg.Name,
		occurredAt: now,
	})
	return t, nil
}

// ID returns the ticket's identifier.
func (t *Ticket) ID() TicketID                      { return t.id }
func (t *Ticket) ConfigurationName() ConfigurationName { return t.configurationName }
func (t *Ticket) ConfigurationARN() string           { return t.configurationARN }
func (t *Ticket) Status() TicketStatus               { return t.status }
func (t *Ticket) StartTime() time.Time               { return t.startTime }
func (t *Ticket) EndTime() time.Time                 { return t.endTime }
func (t *Ticket) Players() []Player                  { return clonePlayers(t.players) }
func (t *Ticket) MatchID() MatchID                   { return t.matchID }
func (t *Ticket) StatusReason() string               { return t.statusReason }
func (t *Ticket) StatusMessage() string              { return t.statusMessage }
func (t *Ticket) CancelRequestedByAPI() bool         { return t.cancelByAPI }
func (t *Ticket) EstimatedWait() *time.Duration      { return t.estimatedWait }

// PullEvents returns and clears the events accumulated since the last pull.
func (t *Ticket) PullEvents() []Event {
	out := t.events
	t.events = nil
	return out
}

// Intent-revealing state transitions. Each enforces the state-machine rule
// via status.canTransitionTo and records the corresponding domain event.

// AssignToProposal moves a QUEUED/SEARCHING ticket into REQUIRES_ACCEPTANCE
// when flexi forms a proposal that involves this ticket.
func (t *Ticket) AssignToProposal(matchID MatchID, now time.Time) error {
	if err := t.transition(StatusRequiresAcceptance, now); err != nil {
		return err
	}
	t.matchID = matchID
	t.recordEvent(EventTicketAssignedToProposal{
		ticketID: t.id, configName: t.configurationName, matchID: matchID, occurredAt: now,
	})
	return nil
}

// AcceptPlayer records a per-player acceptance; does not change status but
// emits a PlayerAcceptanceRecorded event so publishers can fan out an AcceptMatch
// notification. Requires the ticket to currently be in REQUIRES_ACCEPTANCE.
func (t *Ticket) RecordPlayerAcceptance(playerID PlayerID, accepted bool, now time.Time) error {
	if t.status != StatusRequiresAcceptance {
		return fmt.Errorf("%w: ticket %s is not in REQUIRES_ACCEPTANCE", ErrInvalidTransition, t.id)
	}
	if !t.hasPlayer(playerID) {
		return fmt.Errorf("%w: %s", ErrPlayerNotInTicket, playerID)
	}
	t.recordEvent(EventPlayerAcceptanceRecorded{
		ticketID:   t.id,
		configName: t.configurationName,
		matchID:    t.matchID,
		playerID:   playerID,
		accepted:   accepted,
		occurredAt: now,
	})
	return nil
}

// AcceptanceCompleted finalizes acceptance with the given outcome. Used by
// the application layer after observing flexi's resolution (all accepted,
// rejected, or timed out). The status change to the next lifecycle state is
// performed in separate transitions (MoveToPlacing / MarkFailed / MarkTimedOut).
func (t *Ticket) AcceptanceCompleted(outcome AcceptanceOutcome, now time.Time) {
	t.recordEvent(EventAcceptanceCompleted{
		ticketID: t.id, configName: t.configurationName, matchID: t.matchID, outcome: outcome, occurredAt: now,
	})
}

// MoveToPlacing transitions an accepted proposal or direct match into PLACING.
func (t *Ticket) MoveToPlacing(matchID MatchID, now time.Time) error {
	if err := t.transition(StatusPlacing, now); err != nil {
		return err
	}
	if matchID != "" {
		t.matchID = matchID
	}
	return nil
}

// Complete marks the ticket COMPLETED and emits Succeeded.
func (t *Ticket) Complete(now time.Time) error {
	if err := t.transition(StatusCompleted, now); err != nil {
		return err
	}
	t.endTime = now
	t.recordEvent(EventMatchmakingSucceeded{
		ticketID: t.id, configName: t.configurationName, matchID: t.matchID, occurredAt: now,
	})
	return nil
}

// MarkFailed is used when a proposal is rejected by another ticket's player.
func (t *Ticket) MarkFailed(reason, message string, now time.Time) error {
	if err := t.transition(StatusFailed, now); err != nil {
		return err
	}
	t.endTime = now
	t.statusReason = reason
	t.statusMessage = message
	t.recordEvent(EventMatchmakingFailed{
		ticketID: t.id, configName: t.configurationName, matchID: t.matchID, reason: reason, message: message, occurredAt: now,
	})
	return nil
}

// MarkTimedOut is used for request timeout (no match formed within budget) or
// acceptance timeout (proposal not accepted in time).
func (t *Ticket) MarkTimedOut(reason, message string, now time.Time) error {
	if err := t.transition(StatusTimedOut, now); err != nil {
		return err
	}
	t.endTime = now
	t.statusReason = reason
	t.statusMessage = message
	t.recordEvent(EventMatchmakingTimedOut{
		ticketID: t.id, configName: t.configurationName, matchID: t.matchID, reason: reason, message: message, occurredAt: now,
	})
	return nil
}

// MarkCancelledViaReject is used when the ticket's proposal is dissolved
// because another player rejected. From the ticket's perspective the status
// is CANCELLED (matching flexi) but the emitted event is MatchmakingFailed.
func (t *Ticket) MarkCancelledViaReject(now time.Time) error {
	if err := t.transition(StatusCancelled, now); err != nil {
		return err
	}
	t.endTime = now
	t.statusReason = "Rejected"
	t.statusMessage = "Proposal was rejected"
	t.recordEvent(EventMatchmakingFailed{
		ticketID: t.id, configName: t.configurationName, matchID: t.matchID, reason: "Rejected", message: "Proposal was rejected", occurredAt: now,
	})
	return nil
}

// RequestCancel is called by the application when the user invokes
// StopMatchmaking. It records intent; actual engine-driven status change
// happens later when the engine acknowledges the cancellation.
func (t *Ticket) RequestCancel() { t.cancelByAPI = true }

// MarkCancelledByAPI transitions the ticket to CANCELLED because the user
// asked for it. If RequestCancel was never called the transition still
// succeeds — the application ensures ordering.
func (t *Ticket) MarkCancelledByAPI(now time.Time) error {
	if err := t.transition(StatusCancelled, now); err != nil {
		return err
	}
	t.endTime = now
	t.statusReason = "Cancelled"
	t.statusMessage = "Matchmaking stopped by client"
	t.recordEvent(EventMatchmakingCancelled{
		ticketID: t.id, configName: t.configurationName, matchID: t.matchID, occurredAt: now,
	})
	return nil
}

// ObserveSearching is a no-op status tick used when the engine still reports
// the ticket as actively searching. The Ticket aggregate uses it to suppress
// duplicate MatchmakingSearching events after the first enqueue.
func (t *Ticket) ObserveSearching() {
	t.status = StatusSearching
}

// transition updates the status after checking the state machine.
func (t *Ticket) transition(next TicketStatus, now time.Time) error {
	if !t.status.canTransitionTo(next) {
		return fmt.Errorf("%w: %s -> %s", ErrInvalidTransition, t.status, next)
	}
	t.status = next
	_ = now // reserved for future observability fields
	return nil
}

func (t *Ticket) hasPlayer(id PlayerID) bool {
	for _, p := range t.players {
		if p.ID == string(id) {
			return true
		}
	}
	return false
}

func (t *Ticket) recordEvent(e Event) { t.events = append(t.events, e) }

// RebuildTicket is used by infrastructure to hydrate an aggregate from
// persistence without triggering events. It skips invariant checks and does
// NOT accumulate events; it is intended for in-memory repository re-loads.
func RebuildTicket(
	id TicketID,
	cfgName ConfigurationName,
	cfgARN string,
	players []Player,
	status TicketStatus,
	startTime, endTime time.Time,
	matchID MatchID,
	cancelByAPI bool,
	statusReason, statusMessage string,
	estimatedWait *time.Duration,
) *Ticket {
	return &Ticket{
		id:                id,
		configurationName: cfgName,
		configurationARN:  cfgARN,
		players:           clonePlayers(players),
		status:            status,
		startTime:         startTime,
		endTime:           endTime,
		matchID:           matchID,
		cancelByAPI:       cancelByAPI,
		statusReason:      statusReason,
		statusMessage:     statusMessage,
		estimatedWait:     estimatedWait,
		searchingEmitted:  true,
	}
}
