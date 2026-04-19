// Command fmlocal is the composition root for the local AWS GameLift FlexMatch
// compatible server. It wires domain, application, infrastructure, and
// interface layers together and runs them until interrupted.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"sync"
	"syscall"

	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/sqs"
	"github.com/moepig/flexi"

	"github.com/moepig/fmlocal/internal/app/defaults/idgen"
	"github.com/moepig/fmlocal/internal/app/defaults/sysclock"
	appmm "github.com/moepig/fmlocal/internal/app/matchmaking"
	"github.com/moepig/fmlocal/internal/app/ports"
	mm "github.com/moepig/fmlocal/internal/domain/matchmaking"
	"github.com/moepig/fmlocal/internal/infrastructure/notification"
	"github.com/moepig/fmlocal/internal/interfaces/awsapi"
	"github.com/moepig/fmlocal/internal/interfaces/webui"
	"github.com/moepig/fmlocal/internal/system/configfile"
)

var version = "dev"

func main() {
	var configPath string
	flag.StringVar(&configPath, "config", "config.yaml", "path to YAML config")
	flag.Parse()

	// Use a plain Info logger until the config file is parsed and the
	// configured level is known.
	logger := slog.New(slog.NewTextHandler(os.Stderr, nil))

	loaded, err := configfile.LoadFile(configPath)
	if err != nil {
		logger.Error("config load", "err", err.Error())
		os.Exit(1)
	}
	logger = slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: loaded.LogLevel}))
	if err := run(loaded, logger); err != nil {
		logger.Error("fmlocal exited", "err", err.Error())
		os.Exit(1)
	}
}

func run(cfg *configfile.Loaded, logger *slog.Logger) error {
	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer cancel()

	clk := sysclock.System{}
	ids := idgen.NewUUID()

	// Engines — one *flexi.Matchmaker per configuration, built directly.
	resolver := appmm.NewStaticEngineResolver()
	for _, b := range cfg.Configurations {
		rs := findRuleSet(cfg.RuleSets, b.Configuration.RuleSetName)
		engine, err := flexi.New(rs.Body, flexi.WithClock(clk))
		if err != nil {
			return fmt.Errorf("engine for %q: %w", b.Configuration.Name, err)
		}
		resolver.Register(b.Configuration.Name, engine)
	}

	// Application service
	svc := &appmm.Service{
		Engines:  resolver,
		Clock:    clk,
		IDs:      ids,
		MatchIDs: idgen.NewUUID(),
		Logger:   logger,
	}
	svc.LoadConfigurations(mmConfigurations(cfg))
	svc.LoadRuleSets(cfg.RuleSets)

	// Publishers
	publisherByID, err := buildPublishers(ctx, cfg, ids, svc)
	if err != nil {
		return err
	}
	publisherByConfig := map[mm.ConfigurationName]ports.EventPublisher{}
	for _, b := range cfg.Configurations {
		children := make([]ports.EventPublisher, 0, len(b.PublisherIDs))
		for _, pid := range b.PublisherIDs {
			if p, ok := publisherByID[pid]; ok {
				children = append(children, p)
			}
		}
		var pub ports.EventPublisher = notification.Noop{}
		if len(children) > 0 {
			pub = notification.NewMulti(children...)
		}
		publisherByConfig[b.Configuration.Name] = pub
	}
	svc.Publishers = publisherByConfig

	// Interface adapters
	apiSrv := awsapi.NewServer(svc, awsapi.Options{AWSAPIPort: cfg.AWSAPIPort}, logger)
	uiSrv := webui.NewServer(svc, webui.Options{
		WebUIPort: cfg.WebUIPort,
		Region:    cfg.Region,
		AccountID: cfg.AccountID,
	})

	// Ticker
	names := make([]mm.ConfigurationName, 0, len(cfg.Configurations))
	for _, b := range cfg.Configurations {
		names = append(names, b.Configuration.Name)
	}
	ticker := &appmm.Ticker{Service: svc, Names: names, Logger: logger}

	var wg sync.WaitGroup
	errCh := make(chan error, 3)

	wg.Add(1)
	go func() {
		defer wg.Done()
		logger.Info("starting AWS API server", "port", cfg.AWSAPIPort)
		if err := apiSrv.Run(ctx); err != nil && !errors.Is(err, http.ErrServerClosed) {
			errCh <- fmt.Errorf("aws api: %w", err)
		}
	}()
	wg.Add(1)
	go func() {
		defer wg.Done()
		logger.Info("starting Web UI server", "port", cfg.WebUIPort)
		if err := uiSrv.Run(ctx); err != nil && !errors.Is(err, http.ErrServerClosed) {
			errCh <- fmt.Errorf("webui: %w", err)
		}
	}()
	wg.Add(1)
	go func() {
		defer wg.Done()
		if err := ticker.Run(ctx, cfg.TickInterval); err != nil {
			errCh <- fmt.Errorf("ticker: %w", err)
		}
	}()

	wg.Wait()
	close(errCh)
	var joined error
	for e := range errCh {
		joined = errors.Join(joined, e)
	}
	return joined
}

func mmConfigurations(cfg *configfile.Loaded) []mm.Configuration {
	out := make([]mm.Configuration, 0, len(cfg.Configurations))
	for _, b := range cfg.Configurations {
		out = append(out, b.Configuration)
	}
	return out
}

func findRuleSet(list []mm.RuleSet, name mm.RuleSetName) mm.RuleSet {
	for _, rs := range list {
		if rs.Name == name {
			return rs
		}
	}
	return mm.RuleSet{Name: name}
}

func buildPublishers(ctx context.Context, cfg *configfile.Loaded, ids ports.IDGenerator, svc *appmm.Service) (map[string]ports.EventPublisher, error) {
	out := map[string]ports.EventPublisher{}
	settings := notification.EnvelopeSettings{Region: cfg.Region, AccountID: cfg.AccountID}
	lookup := ticketLookup(svc)
	for _, p := range cfg.Publishers {
		if !p.Enabled {
			continue
		}
		translator := notification.NewTranslator(ids, settings, lookup)
		var pub ports.EventPublisher
		switch p.Kind {
		case configfile.PublisherKindSNSHTTP:
			pub = notification.NewSNSHTTPPublisher(p.URL, translator, ids, http.DefaultClient)
		case configfile.PublisherKindSQSEventBridge:
			awsCfg, err := awsconfig.LoadDefaultConfig(ctx,
				awsconfig.WithRegion(defaultString(p.AWSRegion, "us-east-1")),
				awsconfig.WithCredentialsProvider(credentials.NewStaticCredentialsProvider(
					defaultString(p.AccessKey, "x"),
					defaultString(p.SecretKey, "x"),
					"",
				)),
			)
			if err != nil {
				return nil, fmt.Errorf("publisher %q load aws cfg: %w", p.ID, err)
			}
			client := sqs.NewFromConfig(awsCfg, func(o *sqs.Options) {
				if p.AWSEndpoint != "" {
					o.BaseEndpoint = &p.AWSEndpoint
				}
			})
			pub = notification.NewSQSEventBridgePublisher(p.QueueURL, translator, client)
		default:
			return nil, fmt.Errorf("publisher %q: unsupported kind %q", p.ID, p.Kind)
		}
		if len(p.OnlyEvents) > 0 {
			pub = notification.NewFiltered(pub, p.OnlyEvents)
		}
		out[p.ID] = pub
	}
	return out, nil
}

// ticketLookup returns a callback suitable for notification.NewTranslator. It
// reads from the service each time so the payload reflects the ticket's state
// at the moment the event is emitted.
func ticketLookup(svc *appmm.Service) notification.TicketLookup {
	return func(id mm.TicketID) (notification.TicketDetail, bool) {
		t, err := svc.GetTicket(id)
		if err != nil {
			return notification.TicketDetail{}, false
		}
		players := make([]notification.PlayerDetail, 0, len(t.Players()))
		for _, p := range t.Players() {
			players = append(players, notification.PlayerDetail{PlayerID: string(p.ID)})
		}
		return notification.TicketDetail{
			TicketID:  string(t.ID()),
			StartTime: t.StartTime().UTC().Format("2006-01-02T15:04:05Z07:00"),
			Players:   players,
		}, true
	}
}

func defaultString(v, def string) string {
	if v == "" {
		return def
	}
	return v
}
