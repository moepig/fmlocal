package configfile_test

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/moepig/fmlocal/internal/system/configfile"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const ruleSetJSON = `{
  "name": "1v1",
  "ruleLanguageVersion": "1.0",
  "teams": [{"name": "all", "minPlayers": 2, "maxPlayers": 2}]
}`

func writeFixture(t *testing.T, yamlBody string) string {
	t.Helper()
	dir := t.TempDir()
	rsPath := filepath.Join(dir, "ruleset.json")
	require.NoError(t, os.WriteFile(rsPath, []byte(ruleSetJSON), 0o644))
	cfgPath := filepath.Join(dir, "config.yaml")
	require.NoError(t, os.WriteFile(cfgPath, []byte(yamlBody), 0o644))
	return cfgPath
}

func TestLoadFile_Success(t *testing.T) {
	path := writeFixture(t, `
server:
  awsApiPort: 9080
  webUIPort: 9081
  tickInterval: 100ms
matchmakingConfigurations:
  - name: default
    ruleSetName: 1v1
    requestTimeoutSeconds: 60
    flexMatchMode: STANDALONE
    notificationTargets: [sink]
ruleSets:
  - name: 1v1
    path: ruleset.json
publishers:
  - id: sink
    kind: sns_http
    enabled: true
    url: http://localhost:9000/sns
`)
	loaded, err := configfile.LoadFile(path)
	require.NoError(t, err)
	assert.Equal(t, 9080, loaded.AWSAPIPort)
	assert.Equal(t, 100*time.Millisecond, loaded.TickInterval)
	require.Len(t, loaded.Configurations, 1)
	assert.Equal(t, []string{"sink"}, loaded.Configurations[0].PublisherIDs)
	require.Len(t, loaded.RuleSets, 1)
	assert.Contains(t, string(loaded.RuleSets[0].Body), `"name": "1v1"`)
}

func TestLoadFile_MissingRuleSetReference(t *testing.T) {
	path := writeFixture(t, `
server:
  awsApiPort: 9080
  webUIPort: 9081
matchmakingConfigurations:
  - name: default
    ruleSetName: ghost
ruleSets:
  - name: 1v1
    path: ruleset.json
`)
	_, err := configfile.LoadFile(path)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "unknown ruleset")
}

func TestLoadFile_UnknownPublisherRejected(t *testing.T) {
	path := writeFixture(t, `
server:
  awsApiPort: 9080
  webUIPort: 9081
matchmakingConfigurations:
  - name: default
    ruleSetName: 1v1
    notificationTargets: [ghost]
ruleSets:
  - name: 1v1
    path: ruleset.json
`)
	_, err := configfile.LoadFile(path)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "unknown publisher")
}

func TestLoadFile_DuplicateRuleSetName(t *testing.T) {
	path := writeFixture(t, `
server:
  awsApiPort: 9080
  webUIPort: 9081
ruleSets:
  - name: dup
    path: ruleset.json
  - name: dup
    path: ruleset.json
`)
	_, err := configfile.LoadFile(path)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "duplicate ruleset")
}

func TestLoadFile_DuplicatePublisherId(t *testing.T) {
	path := writeFixture(t, `
server:
  awsApiPort: 9080
  webUIPort: 9081
ruleSets:
  - name: 1v1
    path: ruleset.json
publishers:
  - id: dup
    kind: sns_http
    enabled: true
    url: http://localhost:9000/sns
  - id: dup
    kind: sns_http
    enabled: true
    url: http://localhost:9001/sns
`)
	_, err := configfile.LoadFile(path)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "duplicate publisher")
}

func TestLoadFile_MissingPublisherKind(t *testing.T) {
	path := writeFixture(t, `
server:
  awsApiPort: 9080
  webUIPort: 9081
ruleSets:
  - name: 1v1
    path: ruleset.json
publishers:
  - id: p1
    enabled: true
    url: http://localhost:9000/sns
`)
	_, err := configfile.LoadFile(path)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "missing kind")
}

func TestLoadFile_UnknownPublisherKind(t *testing.T) {
	path := writeFixture(t, `
server:
  awsApiPort: 9080
  webUIPort: 9081
ruleSets:
  - name: 1v1
    path: ruleset.json
publishers:
  - id: p1
    kind: fax
    enabled: true
`)
	_, err := configfile.LoadFile(path)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "unknown kind")
}

func TestLoadFile_OnlyEventsAccepted(t *testing.T) {
	path := writeFixture(t, `
server:
  awsApiPort: 9080
  webUIPort: 9081
matchmakingConfigurations:
  - name: default
    ruleSetName: 1v1
    flexMatchMode: STANDALONE
    notificationTargets: [sink]
ruleSets:
  - name: 1v1
    path: ruleset.json
publishers:
  - id: sink
    kind: sqs_eventbridge
    enabled: true
    queueUrl: http://elasticmq:9324/000000000000/fmlocal-events
    onlyEvents:
      - MatchmakingSucceeded
      - MatchmakingFailed
`)
	loaded, err := configfile.LoadFile(path)
	require.NoError(t, err)
	require.Len(t, loaded.Publishers, 1)
	assert.Equal(t, []string{"MatchmakingSucceeded", "MatchmakingFailed"}, loaded.Publishers[0].OnlyEvents)
}

func TestLoadFile_OnlyEventsRejectsUnknownName(t *testing.T) {
	path := writeFixture(t, `
server:
  awsApiPort: 9080
  webUIPort: 9081
ruleSets:
  - name: 1v1
    path: ruleset.json
publishers:
  - id: sink
    kind: sqs_eventbridge
    enabled: true
    queueUrl: http://elasticmq:9324/000000000000/fmlocal-events
    onlyEvents: [MatchmakingBogus]
`)
	_, err := configfile.LoadFile(path)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "onlyEvents contains unknown event")
}

func TestLoadFile_FlexMatchModeWithQueueRejected(t *testing.T) {
	path := writeFixture(t, `
server:
  awsApiPort: 9080
  webUIPort: 9081
matchmakingConfigurations:
  - name: default
    ruleSetName: 1v1
    flexMatchMode: WITH_QUEUE
ruleSets:
  - name: 1v1
    path: ruleset.json
`)
	_, err := configfile.LoadFile(path)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "STANDALONE")
}

func TestLoadFile_MissingAWSAPIPort(t *testing.T) {
	path := writeFixture(t, `
server:
  webUIPort: 9081
ruleSets:
  - name: 1v1
    path: ruleset.json
`)
	_, err := configfile.LoadFile(path)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "awsApiPort")
}

func TestLoadFile_PortsMustDiffer(t *testing.T) {
	path := writeFixture(t, `
server:
  awsApiPort: 9080
  webUIPort: 9080
ruleSets:
  - name: 1v1
    path: ruleset.json
`)
	_, err := configfile.LoadFile(path)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "must differ")
}
