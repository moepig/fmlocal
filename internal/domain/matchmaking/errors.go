package matchmaking

import "errors"

var (
	ErrTicketNotFound        = errors.New("matchmaking: ticket not found")
	ErrInvalidRequest        = errors.New("matchmaking: invalid request")
	ErrTicketAlreadyExists   = errors.New("matchmaking: ticket already exists")
	ErrConfigurationNotFound = errors.New("matchmaking: configuration not found")
	ErrRuleSetNotFound       = errors.New("matchmaking: rule set not found")
	ErrInvalidTransition     = errors.New("matchmaking: invalid status transition")
	ErrInvalidRuleSet        = errors.New("matchmaking: invalid rule set")
	ErrProposalNotFound      = errors.New("matchmaking: ticket is not in a pending proposal")
	ErrPlayerNotInTicket     = errors.New("matchmaking: player is not part of this ticket")
	// ErrBackfillInProgress reports that the game session's previous backfill
	// request has already been matched — it awaits acceptance or is being
	// placed — so a new request cannot supersede it.
	ErrBackfillInProgress = errors.New("matchmaking: the game session's previous backfill request is already matched")
)
