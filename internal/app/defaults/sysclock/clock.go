// Package sysclock adapts the application-layer Clock port to the system
// wall-clock (and a controllable fake for tests).
package sysclock

import (
	"sync"
	"time"

	"github.com/moepig/fmlocal/internal/app/ports"
)

type System struct{}

func (System) Now() time.Time { return time.Now() }

type Fake struct {
	mu  sync.Mutex
	now time.Time
}

func NewFake(t time.Time) *Fake { return &Fake{now: t} }

func (c *Fake) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *Fake) Advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = c.now.Add(d)
}

func (c *Fake) Set(t time.Time) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = t
}

// Compile-time assertions that both implement the port.
var (
	_ ports.Clock = System{}
	_ ports.Clock = (*Fake)(nil)
)
