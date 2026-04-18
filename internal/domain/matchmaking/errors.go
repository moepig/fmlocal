package matchmaking

import "errors"

var (
	ErrTicketNotFound        = errors.New("matchmaking: ticket not found")
	ErrTicketAlreadyExists   = errors.New("matchmaking: ticket already exists")
	ErrConfigurationNotFound = errors.New("matchmaking: configuration not found")
	ErrRuleSetNotFound       = errors.New("matchmaking: rule set not found")
	ErrInvalidTransition     = errors.New("matchmaking: invalid status transition")
	ErrInvalidRuleSet        = errors.New("matchmaking: invalid rule set")
	ErrProposalNotFound      = errors.New("matchmaking: ticket is not in a pending proposal")
	ErrPlayerNotInTicket     = errors.New("matchmaking: player is not part of this ticket")
	ErrBackfillUnsupported   = errors.New("matchmaking: backfill is not supported")
)
