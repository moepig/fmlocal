package matchmaking

import (
	"context"
	"errors"
	"fmt"

	"github.com/moepig/flexi"
	mm "github.com/moepig/fmlocal/internal/domain/matchmaking"
)

func (s *Service) StopMatchmaking(_ context.Context, cmd StopMatchmakingCommand) error {
	ticket, err := s.GetTicket(cmd.TicketID)
	if err != nil {
		return err
	}
	engine, err := s.Engines.EngineFor(ticket.ConfigurationName())
	if err != nil {
		return err
	}
	ticket.RequestCancel()
	if err := engine.Cancel(string(ticket.ID())); err != nil {
		if errors.Is(err, flexi.ErrUnknownTicket) {
			return mm.ErrTicketNotFound
		}
		return fmt.Errorf("engine cancel: %w", err)
	}
	return s.SaveTicket(ticket)
}
