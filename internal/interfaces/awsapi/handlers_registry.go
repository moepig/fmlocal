package awsapi

import (
	"net/http"

	appmm "github.com/moepig/fmlocal/internal/app/matchmaking"
	mm "github.com/moepig/fmlocal/internal/domain/matchmaking"
)

func (s *Server) handleDescribeConfigurations(r *http.Request, body []byte) (any, error) {
	var in DescribeMatchmakingConfigurationsInput
	if err := decodeJSON(body, &in); err != nil {
		return nil, err
	}
	cfgs, err := s.service.DescribeConfigurations(r.Context(), appmm.DescribeConfigurationsQuery{
		Names:       mm.ToTyped[mm.ConfigurationName](in.Names),
		RuleSetName: mm.RuleSetName(in.RuleSetName),
	})
	if err != nil {
		return nil, err
	}
	out := DescribeMatchmakingConfigurationsOutput{Configurations: make([]MatchmakingConfiguration, 0, len(cfgs))}
	for _, c := range cfgs {
		out.Configurations = append(out.Configurations, configurationToDTO(c))
	}
	return out, nil
}

func (s *Server) handleDescribeRuleSets(r *http.Request, body []byte) (any, error) {
	var in DescribeMatchmakingRuleSetsInput
	if err := decodeJSON(body, &in); err != nil {
		return nil, err
	}
	rs, err := s.service.DescribeRuleSets(r.Context(), appmm.DescribeRuleSetsQuery{Names: mm.ToTyped[mm.RuleSetName](in.Names)})
	if err != nil {
		return nil, err
	}
	out := DescribeMatchmakingRuleSetsOutput{RuleSets: make([]MatchmakingRuleSet, 0, len(rs))}
	for _, x := range rs {
		out.RuleSets = append(out.RuleSets, ruleSetToDTO(x))
	}
	return out, nil
}

func (s *Server) handleValidateRuleSet(r *http.Request, body []byte) (any, error) {
	var in ValidateMatchmakingRuleSetInput
	if err := decodeJSON(body, &in); err != nil {
		return nil, err
	}
	if err := s.service.ValidateRuleSet(r.Context(), appmm.ValidateRuleSetCommand{Body: []byte(in.RuleSetBody)}); err != nil {
		return nil, err
	}
	return ValidateMatchmakingRuleSetOutput{Valid: true}, nil
}
