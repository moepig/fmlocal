package matchmaking

import (
	"fmt"
	"sync"
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
	// mu guards every mutable field below. The aggregate is shared between the
	// per-configuration command/tick path (which mutates it) and lock-free read
	// paths such as DescribeMatchmaking and the web UI (which read it), so each
	// accessor takes a read lock and each mutator a write lock. Immutable fields
	// fixed at construction (id, configurationName, configurationARN, players,
	// startTime) are read without the lock.
	mu sync.RWMutex

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
	estimatedWait     *time.Duration
	playerTeams       map[string]string
	playerAcceptances map[string]bool
	ruleMetrics       []RuleEvaluationMetric

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
	}
	t.recordEvent(EventTicketSearchingStarted{
		ticketID:   id,
		configName: cfg.Name,
		occurredAt: now,
	})
	return t, nil
}

// ID returns the ticket's identifier. id is immutable, so no lock is needed.
func (t *Ticket) ID() TicketID                         { return t.id }
func (t *Ticket) ConfigurationName() ConfigurationName { return t.configurationName }
func (t *Ticket) ConfigurationARN() string             { return t.configurationARN }
func (t *Ticket) StartTime() time.Time                 { return t.startTime }
func (t *Ticket) Players() []Player                    { return clonePlayers(t.players) }

func (t *Ticket) Status() TicketStatus {
	t.mu.RLock()
	defer t.mu.RUnlock()
	return t.status
}

func (t *Ticket) EndTime() time.Time {
	t.mu.RLock()
	defer t.mu.RUnlock()
	return t.endTime
}

func (t *Ticket) MatchID() MatchID {
	t.mu.RLock()
	defer t.mu.RUnlock()
	return t.matchID
}

func (t *Ticket) StatusReason() string {
	t.mu.RLock()
	defer t.mu.RUnlock()
	return t.statusReason
}

func (t *Ticket) StatusMessage() string {
	t.mu.RLock()
	defer t.mu.RUnlock()
	return t.statusMessage
}

func (t *Ticket) CancelRequestedByAPI() bool {
	t.mu.RLock()
	defer t.mu.RUnlock()
	return t.cancelByAPI
}

func (t *Ticket) EstimatedWait() *time.Duration {
	t.mu.RLock()
	defer t.mu.RUnlock()
	return t.estimatedWait
}

// PlayerTeam returns the team a player was assigned to when the match formed,
// or "" if no assignment has been recorded (e.g. before a proposal/match).
func (t *Ticket) PlayerTeam(id PlayerID) string {
	t.mu.RLock()
	defer t.mu.RUnlock()
	return t.playerTeams[string(id)]
}

// SetPlayerTeams records the team assignment for players in this ticket. The
// assignment originates from the engine when a proposal or match forms; only
// entries for the ticket's own players are retained.
func (t *Ticket) SetPlayerTeams(teams map[string]string) {
	if len(teams) == 0 {
		return
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.playerTeams == nil {
		t.playerTeams = map[string]string{}
	}
	for _, p := range t.players {
		if team, ok := teams[p.ID]; ok {
			t.playerTeams[p.ID] = team
		}
	}
}

// SetRuleMetrics records the engine's cumulative rule-evaluation metrics for
// this ticket so they can be surfaced in its terminal MatchmakingTimedOut /
// MatchmakingCancelled event.
func (t *Ticket) SetRuleMetrics(metrics []RuleEvaluationMetric) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.ruleMetrics = metrics
}

// PullEvents returns and clears the events accumulated since the last pull.
func (t *Ticket) PullEvents() []Event {
	t.mu.Lock()
	defer t.mu.Unlock()
	out := t.events
	t.events = nil
	return out
}

// Intent-revealing state transitions. Each enforces the state-machine rule
// via status.canTransitionTo and records the corresponding domain event.

// AssignToProposal moves a QUEUED/SEARCHING ticket into REQUIRES_ACCEPTANCE
// when flexi forms a proposal that involves this ticket. The match-level
// PotentialMatchCreated event is emitted by the application layer, which knows
// the proposal's full ticket roster.
func (t *Ticket) AssignToProposal(matchID MatchID, now time.Time) error {
	t.mu.Lock()
	defer t.mu.Unlock()
	if err := t.transition(StatusRequiresAcceptance, now); err != nil {
		return err
	}
	t.matchID = matchID
	// Clear any ACCEPTANCE_FAILED reason carried over from a prior re-queue so a
	// freshly proposed ticket does not report a stale status reason.
	t.statusReason = ""
	return nil
}

// RecordPlayerAcceptance records a per-player acceptance decision against the
// ticket's state. It does not change status or record an event: the match-level
// AcceptMatch notification is emitted by the application layer, which has the
// match's full ticket roster. The decision is retained so subsequent AcceptMatch
// events can report the cumulative acceptance status of every player, matching
// AWS. Requires the ticket to be in REQUIRES_ACCEPTANCE.
func (t *Ticket) RecordPlayerAcceptance(playerID PlayerID, accepted bool, now time.Time) error {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.status != StatusRequiresAcceptance {
		return fmt.Errorf("%w: ticket %s is not in REQUIRES_ACCEPTANCE", ErrInvalidTransition, t.id)
	}
	if !t.hasPlayer(playerID) {
		return fmt.Errorf("%w: %s", ErrPlayerNotInTicket, playerID)
	}
	if t.playerAcceptances == nil {
		t.playerAcceptances = map[string]bool{}
	}
	t.playerAcceptances[string(playerID)] = accepted
	return nil
}

// PlayerAcceptances returns the per-player acceptance decisions recorded so far
// (playerID -> accepted). A player absent from the map has not yet responded.
func (t *Ticket) PlayerAcceptances() map[PlayerID]bool {
	t.mu.RLock()
	defer t.mu.RUnlock()
	out := make(map[PlayerID]bool, len(t.playerAcceptances))
	for id, accepted := range t.playerAcceptances {
		out[PlayerID(id)] = accepted
	}
	return out
}

// MoveToPlacing transitions an accepted proposal or direct match into PLACING.
func (t *Ticket) MoveToPlacing(matchID MatchID, now time.Time) error {
	t.mu.Lock()
	defer t.mu.Unlock()
	if err := t.transition(StatusPlacing, now); err != nil {
		return err
	}
	if matchID != "" {
		t.matchID = matchID
	}
	return nil
}

// Complete marks the ticket COMPLETED. The match-level MatchmakingSucceeded
// event is emitted by the application layer once per match.
func (t *Ticket) Complete(now time.Time) error {
	t.mu.Lock()
	defer t.mu.Unlock()
	if err := t.transition(StatusCompleted, now); err != nil {
		return err
	}
	t.endTime = now
	return nil
}

// MarkFailed transitions the ticket to FAILED and emits EventMatchmakingFailed.
// It is reserved for AWS's MatchmakingFailed semantics — queue-placement and
// internal failures — and is intentionally NOT on the current engine-driven
// flow: acceptance failures terminate as CANCELLED (see
// MarkCancelledByAcceptanceFailure), and request timeouts as TIMED_OUT. The
// method and its event remain part of the model so the publisher taxonomy
// mirrors AWS and a future placement path can surface the event.
func (t *Ticket) MarkFailed(reason, message string, now time.Time) error {
	t.mu.Lock()
	defer t.mu.Unlock()
	if err := t.transition(StatusFailed, now); err != nil {
		return err
	}
	t.endTime = now
	t.statusReason = reason
	t.statusMessage = message
	t.recordEvent(EventMatchmakingFailed{
		ticketID: t.id, configName: t.configurationName, matchID: t.matchID, reason: reason, message: message, ruleMetrics: t.ruleMetrics, occurredAt: now,
	})
	return nil
}

// MarkTimedOut is used for request timeout (no match formed within budget) or
// acceptance timeout (proposal not accepted in time).
func (t *Ticket) MarkTimedOut(reason, message string, now time.Time) error {
	t.mu.Lock()
	defer t.mu.Unlock()
	if err := t.transition(StatusTimedOut, now); err != nil {
		return err
	}
	t.endTime = now
	t.statusReason = reason
	t.statusMessage = message
	t.recordEvent(EventMatchmakingTimedOut{
		ticketID: t.id, configName: t.configurationName, matchID: t.matchID, reason: reason, message: message, ruleMetrics: t.ruleMetrics, occurredAt: now,
	})
	return nil
}

// MarkCancelledByAcceptanceFailure is used for the ticket(s) that caused a
// proposal's acceptance to fail — a player who rejected, or who never responded
// before the acceptance timeout elapsed. Following AWS FlexMatch the ticket
// moves to CANCELLED (TIMED_OUT is reserved for the request-level timeout) and
// the emitted event is MatchmakingCancelled, not MatchmakingFailed (which AWS
// uses only for queue-placement / internal failures).
func (t *Ticket) MarkCancelledByAcceptanceFailure(now time.Time) error {
	t.mu.Lock()
	defer t.mu.Unlock()
	if err := t.transition(StatusCancelled, now); err != nil {
		return err
	}
	t.endTime = now
	t.statusReason = "Cancelled"
	t.statusMessage = "A player failed to accept the proposed match"
	t.recordEvent(EventMatchmakingCancelled{
		ticketID: t.id, configName: t.configurationName, matchID: t.matchID, ruleMetrics: t.ruleMetrics, occurredAt: now,
	})
	return nil
}

// ReturnToSearching re-queues a ticket whose players all accepted a proposal
// that then failed acceptance (a sibling ticket rejected or timed out). flexi
// returns such a ticket to the matchmaking pool in SEARCHING; AWS mirrors this
// by re-emitting MatchmakingSearching, which the recorded
// EventTicketSearchingStarted renders. reason is the engine's status reason
// (e.g. ACCEPTANCE_FAILED) surfaced on the ticket so DescribeMatchmaking can
// report why it returned to SEARCHING, matching MatchmakingTicket.StatusReason.
// The ticket's stale proposal association (matchID, per-player acceptances) is
// cleared so the next match starts clean.
func (t *Ticket) ReturnToSearching(reason string, now time.Time) error {
	t.mu.Lock()
	defer t.mu.Unlock()
	if err := t.transition(StatusSearching, now); err != nil {
		return err
	}
	t.matchID = ""
	t.playerAcceptances = nil
	t.statusReason = reason
	t.statusMessage = ""
	t.recordEvent(EventTicketSearchingStarted{
		ticketID:   t.id,
		configName: t.configurationName,
		occurredAt: now,
	})
	return nil
}

// RequestCancel is called by the application when the user invokes
// StopMatchmaking. It records intent; actual engine-driven status change
// happens later when the engine acknowledges the cancellation.
func (t *Ticket) RequestCancel() {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.cancelByAPI = true
}

// MarkCancelledByAPI transitions the ticket to CANCELLED because the user
// asked for it. If RequestCancel was never called the transition still
// succeeds — the application ensures ordering.
func (t *Ticket) MarkCancelledByAPI(now time.Time) error {
	t.mu.Lock()
	defer t.mu.Unlock()
	if err := t.transition(StatusCancelled, now); err != nil {
		return err
	}
	t.endTime = now
	t.statusReason = "Cancelled"
	t.statusMessage = "Matchmaking stopped by client"
	t.recordEvent(EventMatchmakingCancelled{
		ticketID: t.id, configName: t.configurationName, matchID: t.matchID, ruleMetrics: t.ruleMetrics, occurredAt: now,
	})
	return nil
}

// ObserveSearching advances the ticket to SEARCHING when the engine still
// reports it as actively searching. It deliberately records no event, so the
// initial MatchmakingSearching emitted at enqueue is not duplicated on every
// tick the ticket remains in the pool.
func (t *Ticket) ObserveSearching() {
	t.mu.Lock()
	defer t.mu.Unlock()
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
