// Package app — composition root: единственное место, где создаются и связываются зависимости.
package app

import (
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"tg-recs-audit/internal/contract"
	"tg-recs-audit/internal/feature/channelcheck"
	"tg-recs-audit/internal/feature/historycheck"
	"tg-recs-audit/internal/feature/mentions"
	"tg-recs-audit/internal/infra/moexiss"
	"tg-recs-audit/internal/infra/tgexport"
	"tg-recs-audit/internal/infra/tgweb"
)

const dataDir = "data"

type Options struct {
	Cmd     string
	Refs    []string // каналы: t.me/x, @x, x или путь к result.json
	Tickers string
	From    string
	Refresh bool
	Top     int

	// run
	Horizon       string
	PerIdea       float64
	CommissionBps float64
	Offline       bool // не ходить в сеть: посты и цены из кэша
	Dry           bool // не дописывать журнал
	Stack         bool // каждая идея — отдельная позиция (проверка устойчивости)
	NoDividends   bool // ценовой режим: без дивидендов, бенчмарк IMOEX

	// houses
	Split    string
	MinIdeas int

	// diary
	Since      string
	DebtSpread float64
}

func ParseFlags(args []string) (Options, error) {
	if len(args) == 0 {
		return Options{}, fmt.Errorf("команда: run | houses | diary | placements | signals | divevents | check-channel | check-history | prefetch-shares")
	}
	o := Options{Cmd: args[0]}
	fs := flag.NewFlagSet(o.Cmd, flag.ContinueOnError)
	fs.BoolVar(&o.Refresh, "refresh", false, "перекачать кэш (канал целиком, историю цен)")
	fs.IntVar(&o.Top, "top", 25, "сколько тикеров показать")
	fs.StringVar(&o.Tickers, "tickers", "", "check-history: SBER,GAZP вместо каналов")
	fs.StringVar(&o.From, "from", "", "check-history с -tickers: начало периода YYYY-MM-DD")
	fs.StringVar(&o.Horizon, "horizon", "year-end", "run: выход по горизонту year-end | <N>m (1m, 12m)")
	fs.Float64Var(&o.PerIdea, "per-idea", 10000, "run: рублей на идею")
	fs.Float64Var(&o.CommissionBps, "commission-bps", 5, "run: комиссия на каждую ногу, б.п.")
	fs.BoolVar(&o.Offline, "offline", false, "run: без сети, только кэш")
	fs.BoolVar(&o.Dry, "dry", false, "run: не дописывать журнал")
	fs.BoolVar(&o.NoDividends, "no-dividends", false, "run/houses: без дивидендов, бенчмарк IMOEX (как до 2026-10-16)")
	fs.BoolVar(&o.Stack, "stack", false, "run: каждая идея — отдельная позиция, даже если бумага уже в портфеле")
	fs.StringVar(&o.Since, "since", "2023-02-13", "diary: с какого поста считать (2023-02-13 — перезапуск счёта Trendoman)")
	fs.Float64Var(&o.DebtSpread, "debt-spread", 0.04, "diary: долг стоит LQDT + столько годовых")
	fs.StringVar(&o.Split, "split", "2025-01-01", "houses: граница периодов для проверки устойчивости рейтинга")
	fs.IntVar(&o.MinIdeas, "min", 15, "houses: минимум идей у дома")
	if err := fs.Parse(args[1:]); err != nil {
		return o, err
	}
	o.Refs = fs.Args()
	return o, nil
}

// ChannelSource выбирает источник по тому, как канал указан. Новый источник (MTProto) — сюда.
func ChannelSource(ref string, refresh, offline bool) contract.ChannelSource {
	if strings.HasSuffix(ref, ".json") {
		return tgexport.Source{Path: ref}
	}
	if st, err := os.Stat(ref); err == nil && st.IsDir() {
		return tgexport.HTMLSource{Dir: ref}
	}
	src := tgweb.New(channelSlug(ref), filepath.Join(dataDir, "channels"), refresh)
	src.Offline = offline
	return src
}

// channelSlug — короткое имя канала для путей: t.me/ruforecasts → ruforecasts.
func channelSlug(ref string) string {
	if strings.HasSuffix(ref, ".json") {
		return filepath.Base(filepath.Dir(ref))
	}
	if st, err := os.Stat(ref); err == nil && st.IsDir() {
		return filepath.Base(filepath.Clean(ref))
	}
	name := ref
	for _, p := range []string{"https://", "http://", "t.me/s/", "t.me/", "@"} {
		name = strings.TrimPrefix(name, p)
	}
	return strings.Trim(name, "/")
}

func Run(o Options, out io.Writer) error {
	iss := moexiss.New(filepath.Join(dataDir, "cache", "iss"), o.Refresh)
	iss.Offline = o.Offline
	switch o.Cmd {
	case "run":
		return runPortfolio(o, iss, out)
	case "houses":
		return runHouses(o, iss, out)
	case "diary":
		return runDiary(o, iss, out)
	case "placements":
		return runPlacements(o, iss, out)
	case "signals":
		return runSignals(o, iss, out)
	case "divevents":
		return runDivEvents(o, iss, out)
	case "check-channel":
		return checkChannels(o, iss, out)
	case "check-history":
		return checkHistory(o, iss, out)
	case "prefetch-shares":
		return prefetchShares(o, iss, out)
	}
	return fmt.Errorf("неизвестная команда %q", o.Cmd)
}

func loadChannels(o Options) ([]contract.ChannelInfo, [][]contract.Message, error) {
	if len(o.Refs) == 0 {
		return nil, nil, fmt.Errorf("укажите канал: t.me/имя, @имя или путь к result.json")
	}
	var infos []contract.ChannelInfo
	var all [][]contract.Message
	for _, ref := range o.Refs {
		info, msgs, err := ChannelSource(ref, o.Refresh, o.Offline).Messages()
		if err != nil {
			return nil, nil, err
		}
		infos = append(infos, info)
		all = append(all, msgs)
	}
	return infos, all, nil
}

func checkChannels(o Options, iss *moexiss.Client, out io.Writer) error {
	infos, all, err := loadChannels(o)
	if err != nil {
		return err
	}
	universe, err := iss.Shares()
	if err != nil {
		return err
	}
	idx := mentions.NewIndex(universe)
	for i := range infos {
		printChannel(out, channelcheck.Check(infos[i], all[i], idx), o.Top)
	}
	return nil
}

func checkHistory(o Options, iss *moexiss.Client, out io.Writer) error {
	need := map[string][2]time.Time{}
	if o.Tickers != "" {
		from, err := time.Parse(time.DateOnly, o.From)
		if err != nil {
			return fmt.Errorf("-tickers требует -from YYYY-MM-DD: %w", err)
		}
		for _, t := range strings.Split(o.Tickers, ",") {
			need[strings.ToUpper(strings.TrimSpace(t))] = [2]time.Time{from, time.Now()}
		}
	} else {
		_, all, err := loadChannels(o)
		if err != nil {
			return err
		}
		universe, err := iss.Shares()
		if err != nil {
			return err
		}
		var msgs []contract.Message
		for _, m := range all {
			msgs = append(msgs, m...)
		}
		for _, m := range mentions.Find(msgs, mentions.NewIndex(universe)) {
			need[m.SecID] = [2]time.Time{m.First, m.Last}
		}
	}
	reports, err := historycheck.Check(need, iss, time.Now())
	if err != nil {
		return err
	}
	printHistory(out, reports)
	return nil
}

// --- вывод ----------------------------------------------------------------

func printChannel(w io.Writer, r channelcheck.Report, top int) {
	fmt.Fprintf(w, "== %s (%s, источник: %s)\n", r.Info.Title, r.Info.Ref, r.Info.Source)
	if r.Posts == 0 {
		fmt.Fprintln(w, "постов нет")
		return
	}
	years := r.Last.Sub(r.First).Hours() / 24 / 365.25
	fmt.Fprintf(w, "Постов %d, с %s по %s (%.1f г.)\n", r.Posts, r.First.Format(time.DateOnly), r.Last.Format(time.DateOnly), years)
	var ys []int
	for y := range r.PerYear {
		ys = append(ys, y)
	}
	sort.Ints(ys)
	fmt.Fprint(w, "По годам:")
	for _, y := range ys {
		fmt.Fprintf(w, " %d:%d", y, r.PerYear[y])
	}
	fmt.Fprintln(w)

	fmt.Fprintf(w, "Нумерация: %d ID, без поста %d (%.1f%%) — удалённые + служебные (закреп, смена названия)\n",
		r.IDRange, r.Missing, pct(int(r.Missing), int(r.IDRange)))
	for _, h := range r.TopHoles {
		fmt.Fprintf(w, "  дыра %d ID: %d → %d (%s … %s)\n", h.Missing, h.AfterID, h.BeforeID,
			h.From.Format(time.DateOnly), h.To.Format(time.DateOnly))
	}
	editNote := ""
	if !r.Info.EditTimes {
		editNote = " (источник видит только факт правки, не время)"
	}
	fmt.Fprintf(w, "Отредактировано: %d (%.1f%%)%s\n", r.Edited, pct(r.Edited, r.Posts), editNote)
	fmt.Fprintf(w, "С тикером: %d (%.1f%%); с тикером и словом действия: %d (%.1f%%) — грубая оценка доли рекомендаций\n",
		r.WithTicker, pct(r.WithTicker, r.Posts), r.WithAction, pct(r.WithAction, r.Posts))
	fmt.Fprintf(w, "Тикеров: %d. Топ:\n", len(r.Tickers))
	for _, m := range r.Tickers[:min(top, len(r.Tickers))] {
		fmt.Fprintf(w, "  %-6s %4d постов (сильных %d)  %s … %s\n", m.SecID, m.Count, m.Strong,
			m.First.Format(time.DateOnly), m.Last.Format(time.DateOnly))
	}
	fmt.Fprintln(w)
}

func printHistory(w io.Writer, reports []historycheck.Report) {
	rank := map[historycheck.Status]int{historycheck.Missing: 0, historycheck.NotCovered: 1, historycheck.Attention: 2, historycheck.OK: 3}
	sort.Slice(reports, func(i, j int) bool {
		if a, b := rank[reports[i].Status()], rank[reports[j].Status()]; a != b {
			return a < b
		}
		return reports[i].SecID < reports[j].SecID
	})
	fmt.Fprintf(w, "%-8s %-10s %-10s %-10s %-10s %-10s  %s\n", "тикер", "статус", "нужно с", "по", "есть с", "по", "замечания")
	count := map[historycheck.Status]int{}
	for _, r := range reports {
		count[r.Status()]++
		var notes []string
		if r.Stale {
			notes = append(notes, "не торгуется: делистинг или смена тикера")
		}
		for _, g := range r.Gaps {
			notes = append(notes, fmt.Sprintf("дыра %s…%s", g[0].Format(time.DateOnly), g[1].Format(time.DateOnly)))
		}
		for _, s := range r.Splits {
			notes = append(notes, fmt.Sprintf("сплит? %s ×%.4g", s.Day.Format(time.DateOnly), s.Ratio))
		}
		fmt.Fprintf(w, "%-8s %-10s %-10s %-10s %-10s %-10s  %s\n", r.SecID, r.Status(),
			r.Need[0].Format(time.DateOnly), r.Need[1].Format(time.DateOnly), date(r.First), date(r.Last), strings.Join(notes, "; "))
	}
	fmt.Fprintf(w, "\nИтого %d: ok %d, ВНИМАНИЕ %d, НЕ ПОКРЫТ %d, НЕТ %d\n", len(reports),
		count[historycheck.OK], count[historycheck.Attention], count[historycheck.NotCovered], count[historycheck.Missing])
}

func date(t time.Time) string {
	if t.IsZero() {
		return "-"
	}
	return t.Format(time.DateOnly)
}

func pct(a, b int) float64 {
	if b == 0 {
		return 0
	}
	return 100 * float64(a) / float64(b)
}
