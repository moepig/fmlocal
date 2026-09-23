package matchmaking

import (
	"context"
	"sync"
	"time"

	mm "github.com/moepig/fmlocal/internal/domain/matchmaking"
)

const deliveryCapacity = 64
const deliveryTimeout = 10 * time.Second

type deliveryLane struct {
	mu      sync.Mutex
	slots   chan struct{}
	pending []*eventBatch
	running bool
}

func (s *Service) reserveDelivery(ctx context.Context, name mm.ConfigurationName) (*deliveryLane, error) {
	s.deliveryMu.Lock()
	if s.deliveryClosing == nil {
		s.deliveryClosing = make(chan struct{})
		s.deliveryContext, s.deliveryCancel = context.WithCancel(context.Background())
	}
	if s.deliveryClosed {
		s.deliveryMu.Unlock()
		return nil, context.Canceled
	}
	if s.deliveryLanes == nil {
		s.deliveryLanes = map[mm.ConfigurationName]*deliveryLane{}
	}
	lane := s.deliveryLanes[name]
	if lane == nil {
		lane = &deliveryLane{slots: make(chan struct{}, deliveryCapacity)}
		s.deliveryLanes[name] = lane
	}
	closing := s.deliveryClosing
	s.deliveryMu.Unlock()
	select {
	case lane.slots <- struct{}{}:
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-closing:
		return nil, context.Canceled
	}
	s.deliveryMu.Lock()
	if s.deliveryClosed {
		s.deliveryMu.Unlock()
		<-lane.slots
		return nil, context.Canceled
	}
	s.deliveryProducers.Add(1)
	s.deliveryMu.Unlock()
	return lane, nil
}

func (lane *deliveryLane) enqueue(batch *eventBatch) {
	lane.mu.Lock()
	lane.pending = append(lane.pending, batch)
	lane.mu.Unlock()
}

func (lane *deliveryLane) start(s *Service) {
	lane.mu.Lock()
	if !lane.running {
		lane.running = true
		s.deliveryWorkers.Add(1)
		go lane.deliver(s)
	}
	lane.mu.Unlock()
}

func (lane *deliveryLane) deliver(s *Service) {
	defer s.deliveryWorkers.Done()
	for {
		lane.mu.Lock()
		if len(lane.pending) == 0 {
			lane.running = false
			lane.mu.Unlock()
			return
		}
		batch := lane.pending[0]
		lane.pending[0] = nil
		lane.pending = lane.pending[1:]
		lane.mu.Unlock()
		for _, event := range batch.events {
			ctx, cancel := context.WithTimeout(s.deliveryContext, deliveryTimeout)
			s.publishOne(ctx, batch.name, event)
			cancel()
		}
		close(batch.done)
		<-lane.slots
	}
}

func (s *Service) CloseDelivery(ctx context.Context) error {
	s.deliveryMu.Lock()
	if s.deliveryClosing == nil {
		s.deliveryMu.Unlock()
		return nil
	}
	if !s.deliveryClosed {
		s.deliveryClosed = true
		close(s.deliveryClosing)
	}
	s.deliveryMu.Unlock()
	producersDone := make(chan struct{})
	go func() { s.deliveryProducers.Wait(); close(producersDone) }()
	select {
	case <-producersDone:
	case <-ctx.Done():
		s.deliveryCancel()
		return ctx.Err()
	}
	workersDone := make(chan struct{})
	go func() { s.deliveryWorkers.Wait(); close(workersDone) }()
	select {
	case <-workersDone:
		s.deliveryCancel()
		return nil
	case <-ctx.Done():
		s.deliveryCancel()
		<-workersDone
		return ctx.Err()
	}
}
