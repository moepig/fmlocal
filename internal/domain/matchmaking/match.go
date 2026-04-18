package matchmaking

import (
	"time"

	"github.com/moepig/flexi"
)

// Match and Proposal are thin wrappers around flexi's return types with
// TicketID as a typed slice.
type Match struct {
	Teams     map[string][]flexi.Player
	TicketIDs []TicketID
}

type Proposal struct {
	Teams     map[string][]flexi.Player
	TicketIDs []TicketID
	CreatedAt time.Time
}
