package matchmaking

import (
	"context"
	"errors"
	"fmt"

	"github.com/moepig/flexi"
	mm "github.com/moepig/fmlocal/internal/domain/matchmaking"
)

func (s *Service) DescribeConfigurations(_ context.Context, q DescribeConfigurationsQuery) ([]mm.Configuration, error) {
	all := s.ListConfigurations()
	wanted := map[mm.ConfigurationName]bool{}
	for _, n := range q.Names {
		wanted[n] = true
	}
	out := make([]mm.Configuration, 0, len(all))
	for _, c := range all {
		if len(wanted) > 0 && !wanted[c.Name] {
			continue
		}
		if q.RuleSetName != "" && c.RuleSetName != q.RuleSetName {
			continue
		}
		out = append(out, c)
	}
	return out, nil
}

func (s *Service) DescribeRuleSets(_ context.Context, q DescribeRuleSetsQuery) ([]mm.RuleSet, error) {
	all := s.ListRuleSets()
	wanted := map[mm.RuleSetName]bool{}
	for _, n := range q.Names {
		wanted[n] = true
	}
	out := make([]mm.RuleSet, 0, len(all))
	for _, rs := range all {
		if len(wanted) > 0 && !wanted[rs.Name] {
			continue
		}
		out = append(out, rs)
	}
	return out, nil
}

func (s *Service) ValidateRuleSet(_ context.Context, cmd ValidateRuleSetCommand) error {
	if _, err := flexi.New(cmd.Body); err != nil {
		if errors.Is(err, flexi.ErrInvalidRuleSet) {
			return fmt.Errorf("%w: %v", mm.ErrInvalidRuleSet, err)
		}
		return err
	}
	return nil
}
