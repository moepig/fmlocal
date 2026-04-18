// Package idgen adapts the IDGenerator port: UUID by default, deterministic
// sequences for tests.
package idgen

import (
	"fmt"
	"sync/atomic"

	"github.com/google/uuid"
	"github.com/moepig/fmlocal/internal/app/ports"
)

type uuidGen struct{}

func NewUUID() ports.IDGenerator { return uuidGen{} }

func (uuidGen) NewID() string { return uuid.NewString() }

type sequence struct {
	prefix string
	n      atomic.Uint64
}

func NewSequence(prefix string) ports.IDGenerator { return &sequence{prefix: prefix} }

func (s *sequence) NewID() string {
	v := s.n.Add(1)
	return fmt.Sprintf("%s%d", s.prefix, v)
}
