package matchmaking

import (
	"bytes"
	"encoding/json"
	"fmt"

	"github.com/moepig/flexi"
	mm "github.com/moepig/fmlocal/internal/domain/matchmaking"
)

func (s *Service) validateRequiredAttributes(cfg mm.Configuration, players []flexi.Player) error {
	ruleSet, err := s.GetRuleSet(cfg.RuleSetName)
	if err != nil {
		return err
	}
	var body struct {
		PlayerAttributes []struct {
			Name    string          `json:"name"`
			Default json.RawMessage `json:"default"`
		} `json:"playerAttributes"`
	}
	if err := json.Unmarshal(ruleSet.Body, &body); err != nil {
		return fmt.Errorf("%w: parse player attributes: %v", mm.ErrInvalidRuleSet, err)
	}
	for _, attribute := range body.PlayerAttributes {
		value := bytes.TrimSpace(attribute.Default)
		if len(value) != 0 && !bytes.Equal(value, []byte("null")) && !bytes.Equal(value, []byte(`""`)) {
			continue
		}
		for _, player := range players {
			if _, present := player.Attributes[attribute.Name]; !present {
				return fmt.Errorf("%w: player %q requires attribute %q", mm.ErrInvalidRequest, player.ID, attribute.Name)
			}
		}
	}
	return nil
}
