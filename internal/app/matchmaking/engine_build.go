package matchmaking

import (
	"encoding/json"
	"fmt"

	"github.com/moepig/flexi"
	mm "github.com/moepig/fmlocal/internal/domain/matchmaking"
)

// BuildEngine constructs the flexi engine for one configuration.
//
// flexi reads acceptanceRequired / acceptanceTimeoutSeconds /
// requestTimeoutSeconds from non-standard rule set extension fields, but on
// AWS those settings live on the MatchmakingConfiguration and the rule set
// JSON is verbatim FlexMatch. To keep the configuration the single source of
// truth, the values are injected into the rule set body before it is handed
// to flexi; anything the rule set itself declares for them is overridden.
func BuildEngine(cfg mm.Configuration, rs mm.RuleSet, opts ...flexi.Option) (*flexi.Matchmaker, error) {
	body, err := injectEngineSettings(rs.Body, cfg)
	if err != nil {
		return nil, fmt.Errorf("matchmaking: rule set %q: %w", rs.Name, err)
	}
	return flexi.New(body, opts...)
}

// injectEngineSettings overwrites flexi's engine-setting extension fields in a
// rule set document with the configuration's values. A zero duration renders
// as 0, which flexi documents as "no timeout" — matching the configuration's
// own semantics.
func injectEngineSettings(body []byte, cfg mm.Configuration) ([]byte, error) {
	var doc map[string]any
	if err := json.Unmarshal(body, &doc); err != nil {
		return nil, fmt.Errorf("parse rule set body: %w", err)
	}
	doc["acceptanceRequired"] = cfg.AcceptanceRequired
	doc["acceptanceTimeoutSeconds"] = int(cfg.AcceptanceTimeout.Seconds())
	doc["requestTimeoutSeconds"] = int(cfg.RequestTimeout.Seconds())
	return json.Marshal(doc)
}
