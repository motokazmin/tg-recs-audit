package app

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"tg-recs-audit/internal/contract"
	"tg-recs-audit/internal/feature/paperstrat"
	"tg-recs-audit/internal/infra/moexiss"
	"tg-recs-audit/internal/infra/smartlab"
	"tg-recs-audit/internal/infra/telegram"
	"tg-recs-audit/internal/infra/tgweb"
	"tg-recs-audit/internal/paper"
)

// PaperOptions — флаги и окружение paperd (ADR 0005).
type PaperOptions struct {
	Listen string
	Dir    string
	Start  string
	Hour   int
	Token  string
}

func ParsePaperFlags(args []string) (PaperOptions, error) {
	o := PaperOptions{Token: strings.TrimSpace(os.Getenv("ADMIN_TOKEN"))}
	fs := flag.NewFlagSet("paperd", flag.ContinueOnError)
	listen := os.Getenv("HTTP_LISTEN")
	if listen == "" {
		listen = "127.0.0.1:8092"
	}
	fs.StringVar(&o.Listen, "http-listen", listen, "адрес админки (не localhost — нужен ADMIN_TOKEN)")
	fs.StringVar(&o.Dir, "dir", filepath.Join(dataDir, "paper"), "журналы и память стратегий")
	fs.StringVar(&o.Start, "start", "", "день старта YYYY-MM-DD (только при первом запуске; потом берётся из <dir>/start)")
	fs.IntVar(&o.Hour, "hour", 2, "час ежедневного пересчёта по Москве")
	return o, fs.Parse(args)
}

// startDay — день старта фиксируется при первом запуске и больше не меняется:
// иначе пересчёт с другого дня переписал бы журналы.
func startDay(dir, flagValue string) (time.Time, error) {
	path := filepath.Join(dir, "start")
	if b, err := os.ReadFile(path); err == nil {
		return time.Parse(time.DateOnly, strings.TrimSpace(string(b)))
	} else if !errors.Is(err, os.ErrNotExist) {
		return time.Time{}, err
	}
	d := paper.Today()
	if flagValue != "" {
		var err error
		if d, err = time.Parse(time.DateOnly, flagValue); err != nil {
			return time.Time{}, fmt.Errorf("-start: %w", err)
		}
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return time.Time{}, err
	}
	return d, os.WriteFile(path, []byte(d.Format(time.DateOnly)+"\n"), 0o644)
}

type benchSource struct{ iss *moexiss.Client }

func (b benchSource) Bars(name string) ([]contract.Bar, error) {
	if name == "LQDT" {
		return b.iss.DailyBars(name)
	}
	return b.iss.IndexBars(name)
}

// RunPaper — сборка и запуск сервиса.
func RunPaper(o PaperOptions) error {
	if err := paper.ValidateListen(o.Listen, o.Token); err != nil {
		return err
	}
	start, err := startDay(o.Dir, o.Start)
	if err != nil {
		return err
	}
	iss := moexiss.New(filepath.Join(dataDir, "cache", "iss"), false)
	sl := smartlab.New(filepath.Join(dataDir, "cache", "smartlab"))
	sl.FreshFor = 20 * time.Hour
	universe, err := iss.Shares()
	if err != nil {
		return err
	}
	known := map[string]bool{}
	for _, s := range universe {
		if s.Type == "common_share" || s.Type == "preferred_share" || s.Type == "depositary_receipt" {
			known[s.SecID] = true
		}
	}
	lqdt := func() ([]contract.Bar, error) { return iss.DailyBars("LQDT") }
	strategies := []paper.Strategy{
		&paperstrat.Dividends{StartDay: start, Cap: 1_000_000, PerTrade: 50_000,
			Source: tgweb.New("divonline", filepath.Join(dataDir, "channels"), false), Known: known, Prices: iss, Divs: sl},
		&paperstrat.Placements{Slug: "placements", Heading: "Размещения облигаций по номиналу (идея 1)",
			StartDay: start, Cap: 1_000_000, PerTrade: 20_000, Universe: iss.Bonds, Card: iss.Card, Bonds: iss, MM: lqdt},
		&paperstrat.Placements{Slug: "placements-falling", Heading: "Размещения — только при снижении ставки (0012)",
			StartDay: start, Cap: 1_000_000, PerTrade: 20_000, FallingOnly: true, Universe: iss.Bonds, Card: iss.Card, Bonds: iss, MM: lqdt},
		&paperstrat.OFZAuctions{StartDay: start, Cap: 1_000_000, PerTrade: 500_000,
			Auctions: iss.Auctions, Bars: iss.OFZBars, Flows: iss.BondFlows, MM: lqdt},
	}
	runner := paper.NewRunner(o.Dir, strategies, telegram.FromEnv())
	srv := &http.Server{Addr: o.Listen, Handler: paper.NewServer(runner, benchSource{iss}, o.Token).Handler(), ReadHeaderTimeout: 10 * time.Second}

	stop := make(chan struct{})
	go runner.Schedule(o.Hour, stop)
	go func() {
		log.Printf("paperd: старт %s, админка http://%s", start.Format(time.DateOnly), o.Listen)
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Fatalf("paperd: %v", err)
		}
	}()
	sig := make(chan os.Signal, 1)
	signal.Notify(sig, syscall.SIGINT, syscall.SIGTERM)
	<-sig
	close(stop)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	return srv.Shutdown(ctx)
}
