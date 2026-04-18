package matchmaking

import (
	"context"

	mm "github.com/moepig/fmlocal/internal/domain/matchmaking"
)

func (s *Service) DescribeMatchmaking(_ context.Context, q DescribeMatchmakingQuery) ([]*mm.Ticket, error) {
	return s.GetTickets(q.TicketIDs), nil
}
