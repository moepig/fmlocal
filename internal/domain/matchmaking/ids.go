// Package matchmaking is the domain layer for fmlocal's matchmaking bounded
// context. It describes the language of the domain — tickets, configurations,
// rule sets, matches, statuses, and the events that fire as tickets move
// through their lifecycle — in terms that are independent of any particular
// infrastructure (flexi, AWS, storage). Adapters live in internal/infrastructure.
package matchmaking

import (
	"errors"
	"strings"
)

// TicketID identifies a single matchmaking ticket.
type TicketID string

func NewTicketID(v string) (TicketID, error) {
	if strings.TrimSpace(v) == "" {
		return "", errors.New("matchmaking: ticket id must not be empty")
	}
	return TicketID(v), nil
}

func (id TicketID) String() string { return string(id) }

// PlayerID identifies a player within a ticket's roster.
type PlayerID string

func (id PlayerID) String() string { return string(id) }

// MatchID is assigned by the application when a proposal first forms.
type MatchID string

func (id MatchID) String() string { return string(id) }

// ConfigurationName is the human-facing key of a MatchmakingConfiguration.
type ConfigurationName string

func (n ConfigurationName) String() string { return string(n) }

// RuleSetName is the human-facing key of a RuleSet.
type RuleSetName string

func (n RuleSetName) String() string { return string(n) }
