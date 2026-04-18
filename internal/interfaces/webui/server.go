// Package webui is the inbound HTTP adapter that renders the read-only
// operator view. HTML templates live in templates/ and are embedded at
// compile time via embed.FS so the binary is self-contained.
package webui

import (
	"context"
	_ "embed"
	"fmt"
	"html/template"
	"net/http"
	"time"

	appmm "github.com/moepig/fmlocal/internal/app/matchmaking"
	mm "github.com/moepig/fmlocal/internal/domain/matchmaking"
)

//go:embed templates/index.html
var tmplIndex string

//go:embed templates/rulesets.html
var tmplRuleSets string

//go:embed templates/ruleset.html
var tmplRuleSet string

//go:embed templates/pools.html
var tmplPools string

//go:embed templates/pool.html
var tmplPool string

var (
	indexTmpl    = template.Must(template.New("index").Parse(tmplIndex))
	ruleSetsTmpl = template.Must(template.New("rulesets").Parse(tmplRuleSets))
	ruleSetTmpl  = template.Must(template.New("ruleset").Parse(tmplRuleSet))
	poolsTmpl    = template.Must(template.New("pools").Parse(tmplPools))
	poolTmpl     = template.Must(template.New("pool").Parse(tmplPool))
)

type Options struct {
	WebUIPort int
	Region    string
	AccountID string
}

// Server serves the plain-HTML operator UI.
type Server struct {
	Service *appmm.Service
	Options Options
}

func NewServer(svc *appmm.Service, opts Options) *Server {
	return &Server{Service: svc, Options: opts}
}

func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /{$}", s.handleIndex)
	mux.HandleFunc("GET /rulesets", s.handleRuleSets)
	mux.HandleFunc("GET /rulesets/{name}", s.handleRuleSet)
	mux.HandleFunc("GET /pools", s.handlePools)
	mux.HandleFunc("GET /pools/{name}", s.handlePool)
	return mux
}

func (s *Server) Run(ctx context.Context) error {
	srv := &http.Server{
		Addr:              fmt.Sprintf(":%d", s.Options.WebUIPort),
		Handler:           s.Handler(),
		ReadHeaderTimeout: 5 * time.Second,
	}
	errCh := make(chan error, 1)
	go func() { errCh <- srv.ListenAndServe() }()
	select {
	case <-ctx.Done():
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		return srv.Shutdown(shutdownCtx)
	case err := <-errCh:
		return err
	}
}

func (s *Server) handleIndex(w http.ResponseWriter, _ *http.Request) {
	_ = indexTmpl.Execute(w, map[string]any{
		"Region":    s.Options.Region,
		"AccountID": s.Options.AccountID,
	})
}

func (s *Server) handleRuleSets(w http.ResponseWriter, _ *http.Request) {
	rs := s.Service.ListRuleSets()
	view := make([]struct{ Name string }, 0, len(rs))
	for _, x := range rs {
		view = append(view, struct{ Name string }{string(x.Name)})
	}
	_ = ruleSetsTmpl.Execute(w, map[string]any{"RuleSets": view})
}

func (s *Server) handleRuleSet(w http.ResponseWriter, r *http.Request) {
	rs, err := s.Service.GetRuleSet(mm.RuleSetName(r.PathValue("name")))
	if err != nil {
		http.NotFound(w, r)
		return
	}
	_ = ruleSetTmpl.Execute(w, map[string]any{"Name": rs.Name, "Body": string(rs.Body)})
}

type poolRow struct {
	Name        string
	RuleSetName string
	Active      int
}

func (s *Server) handlePools(w http.ResponseWriter, _ *http.Request) {
	configs := s.Service.ListConfigurations()
	rows := make([]poolRow, 0, len(configs))
	for _, c := range configs {
		active := s.Service.ActiveTicketIDsByConfiguration(c.Name)
		rows = append(rows, poolRow{
			Name:        string(c.Name),
			RuleSetName: string(c.RuleSetName),
			Active:      len(active),
		})
	}
	_ = poolsTmpl.Execute(w, map[string]any{"Pools": rows})
}

type poolTicketRow struct {
	TicketID  string
	Status    string
	StartTime string
	MatchID   string
}

func (s *Server) handlePool(w http.ResponseWriter, r *http.Request) {
	name := mm.ConfigurationName(r.PathValue("name"))
	tickets := s.Service.TicketsByConfiguration(name)
	rows := make([]poolTicketRow, 0, len(tickets))
	for _, t := range tickets {
		rows = append(rows, poolTicketRow{
			TicketID:  string(t.ID()),
			Status:    string(t.Status()),
			StartTime: t.StartTime().Format(time.RFC3339),
			MatchID:   string(t.MatchID()),
		})
	}
	_ = poolTmpl.Execute(w, map[string]any{"Name": string(name), "Tickets": rows})
}
