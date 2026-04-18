package matchmaking

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

// canTransition enumerates the allowed transitions; anything else is a domain
// invariant violation. The map is intentionally conservative: it encodes what
// the engine actually drives, not a superset of AWS-possible transitions.
func (s TicketStatus) canTransitionTo(next TicketStatus) bool {
	// Same status is always a no-op.
	if s == next {
		return true
	}
	switch s {
	case StatusQueued:
		switch next {
		case StatusSearching, StatusRequiresAcceptance, StatusPlacing, StatusCancelled, StatusTimedOut:
			return true
		}
	case StatusSearching:
		switch next {
		case StatusRequiresAcceptance, StatusPlacing, StatusCancelled, StatusTimedOut:
			return true
		}
	case StatusRequiresAcceptance:
		switch next {
		case StatusPlacing, StatusCancelled, StatusTimedOut, StatusFailed:
			return true
		}
	case StatusPlacing:
		switch next {
		case StatusCompleted, StatusFailed:
			return true
		}
	}
	return false
}
