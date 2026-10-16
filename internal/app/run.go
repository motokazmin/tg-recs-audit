package app

import (
	"encoding/csv"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"tg-recs-audit/internal/contract"
	"tg-recs-audit/internal/feature/ideas"
	"tg-recs-audit/internal/feature/portfolio"
	"tg-recs-audit/internal/infra/ledgerfile"
	"tg-recs-audit/internal/infra/moexiss"
	"tg-recs-audit/internal/infra/smartlab"
)

// corpActionsPath — ручная таблица сплитов и смен тикеров, коммитится.
var corpActionsPath = filepath.Join(dataDir, "corporate_actions.json")

// Бенчмарк сравнивается с портфелем в одной валюте доходности: с дивидендами — индекс
// полной доходности (MCFTR, брутто), без — ценовой IMOEX.
func benchmarkFor(dividends bool) string {
	if dividends {
		return "MCFTR"
	}
	return "IMOEX"
}

// prepared — всё, что нужно для прогона портфеля: общий путь для run и houses.
type prepared struct {
	ref     string
	info    contract.ChannelInfo
	msgs    []contract.Message
	ideas   []ideas.Idea
	rejects []ideas.Reject
	ca      contract.CorpActions
	bench   []contract.Bar
	params  portfolio.Params
	divs    contract.DividendSource // nil — без дивидендов
	bname   string
	asOf    time.Time
}

func prepare(o Options, iss *moexiss.Client) (prepared, error) {
	var p prepared
	if len(o.Refs) != 1 {
		return p, fmt.Errorf("%s: укажите один канал", o.Cmd)
	}
	p.ref = o.Refs[0]
	horizon, err := portfolio.ParseHorizon(o.Horizon)
	if err != nil {
		return p, err
	}
	p.params = portfolio.Params{PerIdea: o.PerIdea, Horizon: horizon, CommissionBps: o.CommissionBps, Stack: o.Stack}

	if p.info, p.msgs, err = ChannelSource(p.ref, o.Refresh, o.Offline).Messages(); err != nil {
		return p, err
	}
	universe, err := iss.Shares()
	if err != nil {
		return p, err
	}
	known := map[string]bool{}
	for _, s := range universe {
		known[s.SecID] = true
	}
	p.ideas, p.rejects = ideas.Parse(p.msgs, known)

	if p.ca, err = loadCorpActions(); err != nil {
		return p, err
	}
	if err := iss.Prefetch(neededTickers(p.ideas, p.ca)); err != nil {
		return p, err
	}
	p.bname = benchmarkFor(!o.NoDividends)
	if !o.NoDividends {
		sl := smartlab.New(filepath.Join(dataDir, "cache", "smartlab"))
		sl.Offline = o.Offline
		p.divs = sl
	}
	if p.bench, err = iss.IndexBars(p.bname); err != nil {
		return p, err
	}
	if len(p.bench) == 0 {
		return p, fmt.Errorf("нет истории %s", p.bname)
	}
	now := time.Now().In(time.FixedZone("MSK", 3*3600))
	p.asOf = time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.UTC)
	return p, nil
}

func problemsErr(out io.Writer, problems []string) error {
	fmt.Fprintf(out, "Прогон недостоверен — нужны записи в %s:\n", corpActionsPath)
	for _, p := range problems {
		fmt.Fprintln(out, "  "+p)
	}
	return fmt.Errorf("%d проблем с корпоративными действиями, журнал не тронут", len(problems))
}

func runPortfolio(o Options, iss *moexiss.Client, out io.Writer) error {
	p, err := prepare(o, iss)
	if err != nil {
		return err
	}
	res, err := portfolio.Run(p.ideas, iss, p.divs, p.bench, p.ca, p.params, p.asOf)
	if err != nil {
		return err
	}
	if len(res.Problems) > 0 {
		return problemsErr(out, res.Problems)
	}

	// Журнал — только по дням, бар которых уже опубликован и не изменится:
	// всё строго раньше последнего дня индекса.
	name := fmt.Sprintf("%s-%s", channelSlug(p.ref), p.params.Horizon)
	if o.Stack {
		name += "-stack"
	}
	if o.NoDividends {
		name += "-price"
	}
	ledger := ledgerfile.File{Path: filepath.Join(dataDir, "ledger", name+".jsonl")}
	cutoff := p.bench[len(p.bench)-1].Day
	old, err := ledger.Load()
	if err != nil {
		return err
	}
	fresh, err := portfolio.Reconcile(old, portfolio.Ops(res), cutoff)
	if err != nil {
		return err
	}

	if err := writePositions(filepath.Join(dataDir, "analysis", "positions-"+name+".csv"), res); err != nil {
		return err
	}
	printRun(out, p, res)
	printNewOps(out, fresh, ledger.Path, o.Dry)
	if o.Dry {
		return nil
	}
	return ledger.Append(fresh)
}

// writePositions — все позиции прогона для разбора (локальный файл, не журнал).
func writePositions(path string, res portfolio.Result) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	defer f.Close()
	w := csv.NewWriter(f)
	_ = w.Write([]string{"post_id", "posted_at", "idea_date", "analyst", "secid", "secid_now", "target",
		"entry_day", "entry_price", "qty", "exit_day", "exit_price", "exit_qty", "exit_reason", "ret", "bench_ret", "hold_days"})
	ff := func(x float64) string { return strconv.FormatFloat(x, 'f', -1, 64) }
	for _, p := range res.Positions {
		ideaDate := ""
		if !p.Idea.IdeaDate.IsZero() {
			ideaDate = p.Idea.IdeaDate.Format(time.DateOnly)
		}
		_ = w.Write([]string{
			strconv.FormatInt(p.Idea.PostID, 10), p.Idea.PostedAt.Format(time.RFC3339), ideaDate, p.Idea.Analyst,
			p.Idea.SecID, p.SecID, ff(p.Idea.Target), p.EntryDay.Format(time.DateOnly), ff(p.EntryPrice), ff(p.Qty),
			p.ExitDay.Format(time.DateOnly), ff(p.ExitPrice), ff(p.ExitQty), p.ExitReason,
			ff(p.Return()), ff(p.Bench()), ff(p.HoldDays()),
		})
	}
	w.Flush()
	return w.Error()
}

func loadCorpActions() (contract.CorpActions, error) {
	var ca contract.CorpActions
	b, err := os.ReadFile(corpActionsPath)
	if errors.Is(err, os.ErrNotExist) {
		return ca, nil
	}
	if err != nil {
		return ca, err
	}
	if err := json.Unmarshal(b, &ca); err != nil {
		return ca, fmt.Errorf("%s: %w", corpActionsPath, err)
	}
	return ca, nil
}

func neededTickers(all []ideas.Idea, ca contract.CorpActions) []string {
	set := map[string]bool{}
	for _, i := range all {
		set[i.SecID] = true
	}
	for _, r := range ca.Renames {
		set[r.To] = true
	}
	out := make([]string, 0, len(set))
	for s := range set {
		out = append(out, s)
	}
	sort.Strings(out)
	return out
}

// --- вывод ----------------------------------------------------------------

func printRun(w io.Writer, p prepared, res portfolio.Result) {
	divNote := "с дивидендами (брутто)"
	if p.divs == nil {
		divNote = "без дивидендов"
	}
	fmt.Fprintf(w, "== %s · горизонт %s · %.0f ₽ на идею · %s · бенчмарк %s · на %s\n",
		p.info.Title, p.params.Horizon, p.params.PerIdea, divNote, p.bname, res.AsOf.Format(time.DateOnly))
	fmt.Fprintf(w, "Постов %d → идей %d (не разобрано %d)\n", len(p.msgs), len(p.ideas), len(p.rejects))

	skips := map[string]int{}
	for _, s := range res.Skips {
		r := s.Reason
		if strings.HasPrefix(r, "уже в портфеле") {
			r = "уже в портфеле"
		} else if strings.HasPrefix(r, "торги возобновились") {
			r = "долго не было торгов после поста"
		}
		skips[r]++
	}
	fmt.Fprintf(w, "Куплено %d, пропущено %d, ждут открытия торгов %d\n", len(res.Positions), len(res.Skips), len(res.Pending))
	for _, k := range sortedKeys(skips) {
		fmt.Fprintf(w, "  пропуск: %-40s %d\n", k, skips[k])
	}

	var closed, open []portfolio.Position
	byReason := map[string][]portfolio.Position{}
	byYear := map[int][]portfolio.Position{}
	for _, p := range res.Positions {
		if p.Open() {
			open = append(open, p)
		} else {
			closed = append(closed, p)
			byReason[p.ExitReason] = append(byReason[p.ExitReason], p)
		}
		byYear[p.EntryDay.Year()] = append(byYear[p.EntryDay.Year()], p)
	}

	fmt.Fprintln(w)
	header(w, p.bname)
	row(w, "все", portfolio.Summarize(res.Positions))
	row(w, "закрытые", portfolio.Summarize(closed))
	row(w, "открытые (оценка)", portfolio.Summarize(open))
	for _, k := range sortedKeys(byReason) {
		row(w, "  выход: "+k, portfolio.Summarize(byReason[k]))
	}
	var years []int
	for y := range byYear {
		years = append(years, y)
	}
	sort.Ints(years)
	for _, y := range years {
		row(w, fmt.Sprintf("  вход в %d", y), portfolio.Summarize(byYear[y]))
	}
	var dec []portfolio.Position
	for _, p := range res.Positions {
		if p.EntryDay.Month() == time.December {
			dec = append(dec, p)
		}
	}
	row(w, "  вход в декабре", portfolio.Summarize(dec))

	peak, at := portfolio.PeakCapital(res.Positions)
	g := portfolio.Summarize(res.Positions)
	var divs float64
	for _, pos := range res.Positions {
		divs += pos.DividendSum()
	}
	fmt.Fprintf(w, "\nДеньги: на пике вложено %.0f ₽ (%s). P&L %+.0f ₽ = %+.1f%% к пику (из них дивиденды %+.0f ₽); %s теми же деньгами в те же дни %+.0f ₽\n",
		peak, at.Format(time.DateOnly), g.PnL, 100*g.PnL/peak, divs, p.bname, g.BenchPnL)
	fmt.Fprintf(w, "Годовая доходность потока (XIRR): канал %s, %s %s\n",
		pctOrDash(portfolio.XIRR(res.Positions, res.AsOf, false)), p.bname, pctOrDash(portfolio.XIRR(res.Positions, res.AsOf, true)))

	if len(res.Pending) > 0 {
		fmt.Fprintln(w, "\nЖдут открытия торгов (купим по open следующей сессии):")
		for _, i := range res.Pending {
			fmt.Fprintf(w, "  пост %d %s %-6s цель %.2f  %s\n", i.PostID, i.PostedAt.In(time.FixedZone("MSK", 3*3600)).Format("2006-01-02 15:04"), i.SecID, i.Target, i.Analyst)
		}
	}
}

func printNewOps(w io.Writer, ops []contract.Op, path string, dry bool) {
	verb := "Дописано в"
	if dry {
		verb = "Было бы дописано в (-dry)"
	}
	fmt.Fprintf(w, "\n%s %s: %d операций\n", verb, path, len(ops))
	const show = 15
	start := max(0, len(ops)-show)
	if start > 0 {
		fmt.Fprintf(w, "  … первые %d не показаны\n", start)
	}
	for _, o := range ops[start:] {
		switch o.Kind {
		case "skip":
			fmt.Fprintf(w, "  #%d %s skip %-6s пост %d: %s\n", o.Seq, o.Day, o.SecID, o.PostID, o.Reason)
		default:
			fmt.Fprintf(w, "  #%d %s %-4s %-6s %g × %.4g = %.2f ₽ (пост %d) %s\n", o.Seq, o.Day, o.Kind, o.SecID, o.Qty, o.Price, o.Amount, o.PostID, o.Reason)
		}
	}
}

func header(w io.Writer, bench string) {
	fmt.Fprintf(w, "%-22s %5s %6s %8s %8s %9s %7s %12s %12s %12s\n",
		"", "N", "win%", "ср.дох", "медиана", "vs "+bench, "дней", "вложено", "P&L", "P&L "+bench)
}

func row(w io.Writer, name string, g portfolio.Group) {
	if g.N == 0 {
		return
	}
	fmt.Fprintf(w, "%-22s %5d %5.1f%% %+7.1f%% %+7.1f%% %+8.1f%% %7.0f %12.0f %+12.0f %+12.0f\n",
		name, g.N, 100*float64(g.Wins)/float64(g.N), 100*g.MeanRet, 100*g.MedianRet, 100*g.MeanExcess,
		g.MeanHoldDays, g.Invested, g.PnL, g.BenchPnL)
}

func pctOrDash(x float64) string {
	if math.IsNaN(x) {
		return "—"
	}
	return fmt.Sprintf("%+.1f%%", 100*x)
}

func sortedKeys[V any](m map[string]V) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
