package matchmaking

import (
	"cmp"
	"context"
	"fmt"
	"log/slog"
	"maps"
	"slices"
	"sync"

	"github.com/moepig/fmlocal/internal/app/ports"
	mm "github.com/moepig/fmlocal/internal/domain/matchmaking"
)

type Service struct {
	Engines    EngineResolver
	Publishers map[mm.ConfigurationName]ports.EventPublisher
	Clock      ports.Clock
	IDs        ports.IDGenerator
	MatchIDs   ports.IDGenerator
	Logger     *slog.Logger

	stateMu        sync.RWMutex
	tickets        map[mm.TicketID]*mm.Ticket
	configurations map[mm.ConfigurationName]mm.Configuration
	ruleSets       map[mm.RuleSetName]mm.RuleSet

	trackersMu sync.Mutex
	trackers   map[mm.ConfigurationName]*proposalTracker

	cmdMu    sync.Mutex
	cmdLocks map[mm.ConfigurationName]*sync.Mutex
}

// lockConfiguration serializes the whole use-case (engine access, ticket
// read-modify-write and event draining) for a single configuration. FlexMatch
// processes a configuration's pool sequentially, and the shared *flexi.Matchmaker
// is not safe for concurrent use, so every command and every tick for the same
// configuration must run under this lock. Distinct configurations keep their own
// lock, preserving the Ticker's per-configuration parallelism. It returns the
// unlock function so callers can `defer unlock()`.
func (s *Service) lockConfiguration(name mm.ConfigurationName) func() {
	s.cmdMu.Lock()
	if s.cmdLocks == nil {
		s.cmdLocks = map[mm.ConfigurationName]*sync.Mutex{}
	}
	mu, ok := s.cmdLocks[name]
	if !ok {
		mu = &sync.Mutex{}
		s.cmdLocks[name] = mu
	}
	s.cmdMu.Unlock()

	mu.Lock()
	return mu.Unlock
}

// LoadConfigurations installs the configurations fmlocal serves. It replaces
// any previously loaded set; intended for startup wiring.
func (s *Service) LoadConfigurations(cfgs []mm.Configuration) {
	s.stateMu.Lock()
	defer s.stateMu.Unlock()
	s.configurations = make(map[mm.ConfigurationName]mm.Configuration, len(cfgs))
	for _, c := range cfgs {
		s.configurations[c.Name] = c
	}
}

// LoadRuleSets installs the rule sets fmlocal serves. It replaces any
// previously loaded set; intended for startup wiring.
func (s *Service) LoadRuleSets(sets []mm.RuleSet) {
	s.stateMu.Lock()
	defer s.stateMu.Unlock()
	s.ruleSets = make(map[mm.RuleSetName]mm.RuleSet, len(sets))
	for _, rs := range sets {
		s.ruleSets[rs.Name] = rs
	}
}

// GetTicket returns the ticket with id, or mm.ErrTicketNotFound.
func (s *Service) GetTicket(id mm.TicketID) (*mm.Ticket, error) {
	s.stateMu.RLock()
	defer s.stateMu.RUnlock()
	t, ok := s.tickets[id]
	if !ok {
		return nil, mm.ErrTicketNotFound
	}
	return t, nil
}

// GetTickets returns the tickets matching ids, preserving input order and
// silently skipping unknown ids.
func (s *Service) GetTickets(ids []mm.TicketID) []*mm.Ticket {
	s.stateMu.RLock()
	defer s.stateMu.RUnlock()
	out := make([]*mm.Ticket, 0, len(ids))
	for _, id := range ids {
		if t, ok := s.tickets[id]; ok {
			out = append(out, t)
		}
	}
	return out
}

// TicketsByConfiguration returns all tickets belonging to the given
// configuration, sorted by ticket id.
func (s *Service) TicketsByConfiguration(name mm.ConfigurationName) []*mm.Ticket {
	s.stateMu.RLock()
	defer s.stateMu.RUnlock()
	out := make([]*mm.Ticket, 0)
	for _, t := range s.tickets {
		if t.ConfigurationName() == name {
			out = append(out, t)
		}
	}
	slices.SortFunc(out, func(a, b *mm.Ticket) int { return cmp.Compare(a.ID(), b.ID()) })
	return out
}

// ActiveTicketIDsByConfiguration returns the ids of still-active tickets
// belonging to the given configuration, sorted.
func (s *Service) ActiveTicketIDsByConfiguration(name mm.ConfigurationName) []mm.TicketID {
	s.stateMu.RLock()
	defer s.stateMu.RUnlock()
	out := make([]mm.TicketID, 0)
	for _, t := range s.tickets {
		if t.ConfigurationName() != name {
			continue
		}
		if t.Status().IsActive() {
			out = append(out, t.ID())
		}
	}
	slices.Sort(out)
	return out
}

// GetConfiguration returns the configuration by name, or
// mm.ErrConfigurationNotFound.
func (s *Service) GetConfiguration(name mm.ConfigurationName) (mm.Configuration, error) {
	s.stateMu.RLock()
	defer s.stateMu.RUnlock()
	c, ok := s.configurations[name]
	if !ok {
		return mm.Configuration{}, mm.ErrConfigurationNotFound
	}
	return c, nil
}

// ListConfigurations returns all configurations, sorted by name.
func (s *Service) ListConfigurations() []mm.Configuration {
	s.stateMu.RLock()
	defer s.stateMu.RUnlock()
	return slices.SortedFunc(maps.Values(s.configurations),
		func(a, b mm.Configuration) int { return cmp.Compare(a.Name, b.Name) })
}

// GetRuleSet returns the rule set by name, or mm.ErrRuleSetNotFound.
func (s *Service) GetRuleSet(name mm.RuleSetName) (mm.RuleSet, error) {
	s.stateMu.RLock()
	defer s.stateMu.RUnlock()
	rs, ok := s.ruleSets[name]
	if !ok {
		return mm.RuleSet{}, mm.ErrRuleSetNotFound
	}
	return rs, nil
}

// ListRuleSets returns all rule sets, sorted by name.
func (s *Service) ListRuleSets() []mm.RuleSet {
	s.stateMu.RLock()
	defer s.stateMu.RUnlock()
	return slices.SortedFunc(maps.Values(s.ruleSets),
		func(a, b mm.RuleSet) int { return cmp.Compare(a.Name, b.Name) })
}

// SaveTicket is the single write-path for the ticket map.
func (s *Service) SaveTicket(t *mm.Ticket) error {
	if t == nil {
		return fmt.Errorf("matchmaking: nil ticket")
	}
	s.stateMu.Lock()
	defer s.stateMu.Unlock()
	if s.tickets == nil {
		s.tickets = map[mm.TicketID]*mm.Ticket{}
	}
	s.tickets[t.ID()] = t
	return nil
}

func (s *Service) logger() *slog.Logger {
	if s.Logger != nil {
		return s.Logger
	}
	return slog.Default()
}

func (s *Service) publisher(name mm.ConfigurationName) ports.EventPublisher {
	if p, ok := s.Publishers[name]; ok && p != nil {
		return p
	}
	return noopPublisher{}
}

// eventBatch collects the events a command produces while it holds the
// per-configuration command lock. releaseAndFlush publishes them only after the
// lock is released, so blocking publisher I/O (HTTP/SQS) never runs inside the
// critical section and cannot stall other commands or ticks for the same
// configuration. Events are published in the order added, preserving
// per-configuration ordering.
type eventBatch struct {
	name   mm.ConfigurationName
	events []mm.Event
}

func newEventBatch(name mm.ConfigurationName) *eventBatch {
	return &eventBatch{name: name}
}

// add queues a match-level event constructed directly by the application layer.
func (b *eventBatch) add(ev mm.Event) { b.events = append(b.events, ev) }

// addTicket drains and queues the ticket's accumulated domain events. It must be
// called while the command lock is held, since PullEvents mutates the ticket.
func (b *eventBatch) addTicket(t *mm.Ticket) {
	b.events = append(b.events, t.PullEvents()...)
}

// releaseAndFlush is deferred by every command that emits events: it releases
// the command lock and only then publishes the batch, keeping publisher I/O out
// of the critical section. As a deferred call it runs after the command's return
// values have been set, and the batch pointer means events queued after the
// defer statement are still flushed.
func (s *Service) releaseAndFlush(ctx context.Context, unlock func(), b *eventBatch) {
	unlock()
	for _, ev := range b.events {
		s.publishOne(ctx, b.name, ev)
	}
}

// publishOne delivers a single event through the configuration's publisher.
func (s *Service) publishOne(ctx context.Context, name mm.ConfigurationName, ev mm.Event) {
	if err := s.publisher(name).Publish(ctx, ev); err != nil {
		s.logger().Warn("publish event failed",
			"configuration", name, "event", ev.EventName(), "error", err.Error())
	} else {
		s.logger().Debug("publish event",
			"configuration", name, "event", ev.EventName())
	}
}

type noopPublisher struct{}

func (noopPublisher) Publish(_ context.Context, _ mm.Event) error { return nil }
