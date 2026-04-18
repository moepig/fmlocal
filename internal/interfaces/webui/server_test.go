package webui_test

import (
	"io"
	"net/http/httptest"
	"testing"
	"time"

	appmm "github.com/moepig/fmlocal/internal/app/matchmaking"
	mm "github.com/moepig/fmlocal/internal/domain/matchmaking"
	"github.com/moepig/fmlocal/internal/interfaces/webui"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func setup(t *testing.T) *httptest.Server {
	t.Helper()
	svc := &appmm.Service{}
	svc.LoadConfigurations([]mm.Configuration{{Name: "c1", RuleSetName: "1v1"}})
	svc.LoadRuleSets([]mm.RuleSet{{Name: "1v1", Body: []byte(`{"name":"1v1"}`)}})
	tk, err := mm.NewTicket("t1", mm.Configuration{Name: "c1"}, []mm.Player{{ID: "p1"}}, time.Unix(1700000000, 0).UTC())
	require.NoError(t, err)
	require.NoError(t, svc.SaveTicket(tk))

	s := webui.NewServer(svc, webui.Options{Region: "us-east-1", AccountID: "000000000000"})
	srv := httptest.NewServer(s.Handler())
	t.Cleanup(srv.Close)
	return srv
}

func get(t *testing.T, srv *httptest.Server, path string) (int, string) {
	t.Helper()
	resp, err := srv.Client().Get(srv.URL + path)
	require.NoError(t, err)
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, string(body)
}

func TestIndex(t *testing.T)    { _, b := get(t, setup(t), "/"); assert.Contains(t, b, "Rule Sets") }
func TestRuleSets(t *testing.T) { _, b := get(t, setup(t), "/rulesets"); assert.Contains(t, b, "1v1") }
func TestPools(t *testing.T)    { _, b := get(t, setup(t), "/pools"); assert.Contains(t, b, "c1") }
func TestPool(t *testing.T) {
	_, b := get(t, setup(t), "/pools/c1")
	assert.Contains(t, b, "t1")
	assert.Contains(t, b, "QUEUED")
}
