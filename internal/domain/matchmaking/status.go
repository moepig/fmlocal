package matchmaking

import "slices"

// TicketStatus is the finite set of states a matchmaking ticket can be in.
// Values match the AWS GameLift MatchmakingTicket.Status wire strings so
// translation to AWS DTOs is trivial, but the type itself is a domain value
// object — transitions are enforced by the Ticket aggregate, not by callers
// writing to a string.
type TicketStatus string

const (
	StatusQueued             TicketStatus = "QUEUED"
	StatusSearching          TicketStatus = "SEARCHING"
	StatusRequiresAcceptance TicketStatus = "REQUIRES_ACCEPTANCE"
	StatusPlacing            TicketStatus = "PLACING"
	StatusCompleted          TicketStatus = "COMPLETED"
	StatusFailed             TicketStatus = "FAILED"
	StatusCancelled          TicketStatus = "CANCELLED"
	StatusTimedOut           TicketStatus = "TIMED_OUT"
)

func (s TicketStatus) IsTerminal() bool {
	switch s {
	case StatusCompleted, StatusFailed, StatusCancelled, StatusTimedOut:
		return true
	}
	return false
}

func (s TicketStatus) IsActive() bool { return !s.IsTerminal() }

// allowedTransitions enumerates the allowed transitions; anything else is a
// domain invariant violation. The table is intentionally conservative: it
// encodes what the engine actually drives, not a superset of AWS-possible
// transitions.
//
// REQUIRES_ACCEPTANCE -> SEARCHING: a proposal this ticket accepted failed to
// gather every required acceptance, so the engine returns the ticket to the
// pool and AWS re-emits MatchmakingSearching.
var allowedTransitions = map[TicketStatus][]TicketStatus{
	StatusQueued:             {StatusSearching, StatusRequiresAcceptance, StatusPlacing, StatusCancelled, StatusTimedOut},
	StatusSearching:          {StatusRequiresAcceptance, StatusPlacing, StatusCancelled, StatusTimedOut},
	StatusRequiresAcceptance: {StatusSearching, StatusPlacing, StatusCancelled, StatusTimedOut, StatusFailed},
	StatusPlacing:            {StatusCompleted, StatusFailed},
}

func (s TicketStatus) canTransitionTo(next TicketStatus) bool {
	// Same status is always a no-op.
	return s == next || slices.Contains(allowedTransitions[s], next)
}
