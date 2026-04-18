package matchmaking

import (
	"fmt"

	"github.com/moepig/flexi"
	mm "github.com/moepig/fmlocal/internal/domain/matchmaking"
)

// EngineResolver returns the *flexi.Matchmaker for a given configuration.
type EngineResolver interface {
	EngineFor(name mm.ConfigurationName) (*flexi.Matchmaker, error)
}

// StaticEngineResolver is backed by a fixed map populated at startup.
type StaticEngineResolver struct {
	engines map[mm.ConfigurationName]*flexi.Matchmaker
}

func NewStaticEngineResolver() *StaticEngineResolver {
	return &StaticEngineResolver{engines: map[mm.ConfigurationName]*flexi.Matchmaker{}}
}

func (r *StaticEngineResolver) Register(name mm.ConfigurationName, engine *flexi.Matchmaker) {
	r.engines[name] = engine
}

func (r *StaticEngineResolver) EngineFor(name mm.ConfigurationName) (*flexi.Matchmaker, error) {
	e, ok := r.engines[name]
	if !ok {
		return nil, fmt.Errorf("%w: %q", mm.ErrConfigurationNotFound, name)
	}
	return e, nil
}

var _ EngineResolver = (*StaticEngineResolver)(nil)
