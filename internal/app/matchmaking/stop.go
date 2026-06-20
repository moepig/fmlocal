package matchmaking

import (
	"context"
	"errors"
	"fmt"

	"github.com/moepig/flexi"
	mm "github.com/moepig/fmlocal/internal/domain/matchmaking"
)

func (s *Service) StopMatchmaking(ctx context.Context, cmd StopMatchmakingCommand) error {
	ticket, err := s.GetTicket(cmd.TicketID)
	if err != nil {
		return err
	}
	// ConfigurationName is fixed at construction, so reading it before taking the
	// lock is race-free; everything that mutates the ticket runs under the lock.
	name := ticket.ConfigurationName()
	unlock := s.lockConfiguration(name)
	// StopMatchmaking only records cancel intent and tells the engine; the
	// MatchmakingCancelled event is emitted on the next tick once the engine
	// reports the ticket CANCELLED, so the batch stays empty here. It is kept for
	// a uniform command shape (lock, batch, releaseAndFlush) across use cases.
	batch := newEventBatch(name)
	defer s.releaseAndFlush(ctx, unlock, batch)
	engine, err := s.Engines.EngineFor(name)
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
