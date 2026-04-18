package configfile

import (
	"fmt"
	"os"
	"path/filepath"
	"time"

	mm "github.com/moepig/fmlocal/internal/domain/matchmaking"
	"gopkg.in/yaml.v3"
)

// PublisherKind enumerates the publisher types fmlocal can build.
type PublisherKind string

const (
	PublisherKindSNSHTTP        PublisherKind = "sns_http"
	PublisherKindSQSEventBridge PublisherKind = "sqs_eventbridge"
)

// Publisher holds the settings the composition root needs to instantiate an
// EventPublisher adapter. The domain does not know about publishers; this is
// infrastructure configuration.
type Publisher struct {
	ID          string
	Kind        PublisherKind
	Enabled     bool
	URL         string
	QueueURL    string
	AWSEndpoint string
	AWSRegion   string
	AccessKey   string
	SecretKey   string
}

// ConfigurationBinding links a Configuration to its ordered list of publisher
// IDs. The composition root resolves these into concrete adapters.
type ConfigurationBinding struct {
	Configuration mm.Configuration
	PublisherIDs  []string
}

// Loaded is the fully-parsed configuration, ready for the composition root.
type Loaded struct {
	Region       string
	AccountID    string
	AWSAPIPort   int
	WebUIPort    int
	TickInterval time.Duration
	Configurations []ConfigurationBinding
	RuleSets       []mm.RuleSet
	Publishers     []Publisher
}

// LoadFile parses path, resolves rule set bodies, and returns a Loaded with
// defaults applied and cross-references validated.
func LoadFile(path string) (*Loaded, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("configfile: read %s: %w", path, err)
	}
	var doc document
	if err := yaml.Unmarshal(raw, &doc); err != nil {
		return nil, fmt.Errorf("configfile: parse yaml: %w", err)
	}
	applyDefaults(&doc)

	base := filepath.Dir(path)
	ruleSets := make([]mm.RuleSet, 0, len(doc.RuleSets))
	for _, rs := range doc.RuleSets {
		p := rs.Path
		if !filepath.IsAbs(p) {
			p = filepath.Join(base, p)
		}
		body, err := os.ReadFile(p)
		if err != nil {
			return nil, fmt.Errorf("configfile: read ruleset %q at %s: %w", rs.Name, p, err)
		}
		ruleSets = append(ruleSets, mm.RuleSet{
			Name: mm.RuleSetName(rs.Name),
			ARN:  fmt.Sprintf("arn:aws:gamelift:%s:%s:matchmakingruleset/%s", doc.Server.Region, doc.Server.AccountID, rs.Name),
			Body: body,
		})
	}

	publishers := make([]Publisher, 0, len(doc.Publishers))
	for _, p := range doc.Publishers {
		publishers = append(publishers, Publisher{
			ID: p.ID, Kind: PublisherKind(p.Kind), Enabled: p.Enabled,
			URL: p.URL, QueueURL: p.QueueURL,
			AWSEndpoint: p.AWSEndpoint, AWSRegion: p.AWSRegion,
			AccessKey: p.AccessKey, SecretKey: p.SecretKey,
		})
	}

	bindings := make([]ConfigurationBinding, 0, len(doc.MatchmakingConfigurations))
	for _, mc := range doc.MatchmakingConfigurations {
		cfg := mm.Configuration{
			Name:        mm.ConfigurationName(mc.Name),
			ARN:         fmt.Sprintf("arn:aws:gamelift:%s:%s:matchmakingconfiguration/%s", doc.Server.Region, doc.Server.AccountID, mc.Name),
			RuleSetName: mm.RuleSetName(mc.RuleSetName),
			RuleSetARN:  fmt.Sprintf("arn:aws:gamelift:%s:%s:matchmakingruleset/%s", doc.Server.Region, doc.Server.AccountID, mc.RuleSetName),
			RequestTimeout:           time.Duration(mc.RequestTimeoutSeconds) * time.Second,
			AcceptanceRequired:       mc.AcceptanceRequired,
			AcceptanceTimeout:        time.Duration(mc.AcceptanceTimeoutSeconds) * time.Second,
			BackfillMode:             mm.BackfillMode(mc.BackfillMode),
			FlexMatchMode:            mm.FlexMatchMode(defaultString(mc.FlexMatchMode, string(mm.FlexMatchModeStandalone))),
			NotificationTargetIDs:    append([]string(nil), mc.NotificationTargets...),
		}
		bindings = append(bindings, ConfigurationBinding{Configuration: cfg, PublisherIDs: mc.NotificationTargets})
	}

	loaded := &Loaded{
		Region:         doc.Server.Region,
		AccountID:      doc.Server.AccountID,
		AWSAPIPort:     doc.Server.AWSAPIPort,
		WebUIPort:      doc.Server.WebUIPort,
		TickInterval:   doc.Server.TickInterval,
		Configurations: bindings,
		RuleSets:       ruleSets,
		Publishers:     publishers,
	}
	if err := loaded.validate(); err != nil {
		return nil, err
	}
	return loaded, nil
}

func applyDefaults(d *document) {
	if d.Server.Region == "" {
		d.Server.Region = "us-east-1"
	}
	if d.Server.AccountID == "" {
		d.Server.AccountID = "000000000000"
	}
	if d.Server.TickInterval == 0 {
		d.Server.TickInterval = time.Second
	}
}

func (l *Loaded) validate() error {
	if l.AWSAPIPort == 0 {
		return fmt.Errorf("configfile: server.awsApiPort is required")
	}
	if l.WebUIPort == 0 {
		return fmt.Errorf("configfile: server.webUIPort is required")
	}
	if l.AWSAPIPort == l.WebUIPort {
		return fmt.Errorf("configfile: awsApiPort and webUIPort must differ")
	}
	rsNames := map[mm.RuleSetName]bool{}
	for _, rs := range l.RuleSets {
		if rs.Name == "" {
			return fmt.Errorf("configfile: ruleset without a name")
		}
		if rsNames[rs.Name] {
			return fmt.Errorf("configfile: duplicate ruleset %q", rs.Name)
		}
		rsNames[rs.Name] = true
	}
	pubs := map[string]Publisher{}
	for _, p := range l.Publishers {
		if p.ID == "" {
			return fmt.Errorf("configfile: publisher without an id")
		}
		if _, dup := pubs[p.ID]; dup {
			return fmt.Errorf("configfile: duplicate publisher id %q", p.ID)
		}
		switch p.Kind {
		case PublisherKindSNSHTTP:
			if p.URL == "" {
				return fmt.Errorf("configfile: publisher %q (sns_http) requires url", p.ID)
			}
		case PublisherKindSQSEventBridge:
			if p.QueueURL == "" {
				return fmt.Errorf("configfile: publisher %q (sqs_eventbridge) requires queueUrl", p.ID)
			}
		case "":
			return fmt.Errorf("configfile: publisher %q missing kind", p.ID)
		default:
			return fmt.Errorf("configfile: publisher %q unknown kind %q", p.ID, p.Kind)
		}
		pubs[p.ID] = p
	}
	cfgNames := map[mm.ConfigurationName]bool{}
	for _, b := range l.Configurations {
		cfg := b.Configuration
		if cfg.Name == "" {
			return fmt.Errorf("configfile: matchmaking configuration without a name")
		}
		if cfgNames[cfg.Name] {
			return fmt.Errorf("configfile: duplicate matchmaking configuration %q", cfg.Name)
		}
		cfgNames[cfg.Name] = true
		if !rsNames[cfg.RuleSetName] {
			return fmt.Errorf("configfile: matchmaking %q references unknown ruleset %q", cfg.Name, cfg.RuleSetName)
		}
		if cfg.FlexMatchMode != mm.FlexMatchModeStandalone {
			return fmt.Errorf("configfile: matchmaking %q only STANDALONE flexMatchMode is supported, got %q", cfg.Name, cfg.FlexMatchMode)
		}
		for _, id := range b.PublisherIDs {
			p, ok := pubs[id]
			if !ok {
				return fmt.Errorf("configfile: matchmaking %q references unknown publisher %q", cfg.Name, id)
			}
			if !p.Enabled {
				return fmt.Errorf("configfile: matchmaking %q references disabled publisher %q", cfg.Name, id)
			}
		}
	}
	return nil
}

func defaultString(v, def string) string {
	if v == "" {
		return def
	}
	return v
}
