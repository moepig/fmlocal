// Package configfile parses fmlocal's YAML configuration and materializes
// the domain Configuration/RuleSet values along with publisher settings used
// by the composition root to wire infrastructure adapters.
package configfile

import "time"

type document struct {
	Server                    serverSection                    `yaml:"server"`
	MatchmakingConfigurations []matchmakingConfigurationSection `yaml:"matchmakingConfigurations"`
	RuleSets                  []ruleSetSection                 `yaml:"ruleSets"`
	Publishers                []publisherSection               `yaml:"publishers"`
}

type serverSection struct {
	AWSAPIPort   int           `yaml:"awsApiPort"`
	WebUIPort    int           `yaml:"webUIPort"`
	Region       string        `yaml:"region"`
	AccountID    string        `yaml:"accountId"`
	TickInterval time.Duration `yaml:"tickInterval"`
	LogLevel     string        `yaml:"logLevel"`
}

type matchmakingConfigurationSection struct {
	Name                     string   `yaml:"name"`
	RuleSetName              string   `yaml:"ruleSetName"`
	RequestTimeoutSeconds    int      `yaml:"requestTimeoutSeconds"`
	AcceptanceRequired       bool     `yaml:"acceptanceRequired"`
	AcceptanceTimeoutSeconds int      `yaml:"acceptanceTimeoutSeconds"`
	BackfillMode             string   `yaml:"backfillMode"`
	FlexMatchMode            string   `yaml:"flexMatchMode"`
	NotificationTargets      []string `yaml:"notificationTargets"`
}

type ruleSetSection struct {
	Name string `yaml:"name"`
	Path string `yaml:"path"`
}

type publisherSection struct {
	ID          string   `yaml:"id"`
	Kind        string   `yaml:"kind"`
	Enabled     bool     `yaml:"enabled"`
	URL         string   `yaml:"url"`
	QueueURL    string   `yaml:"queueUrl"`
	AWSEndpoint string   `yaml:"awsEndpoint"`
	AWSRegion   string   `yaml:"awsRegion"`
	AccessKey   string   `yaml:"accessKey"`
	SecretKey   string   `yaml:"secretKey"`
	OnlyEvents  []string `yaml:"onlyEvents"`
}
