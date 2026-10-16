package app

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"os"
	"path/filepath"
	"sort"
	"time"

	"tg-recs-audit/internal/contract"
	"tg-recs-audit/internal/feature/diary"
	"tg-recs-audit/internal/feature/portfolio"
	"tg-recs-audit/internal/infra/cbr"
	"tg-recs-audit/internal/infra/ledgerfile"
	"tg-recs-audit/internal/infra/moexiss"
	"tg-recs-audit/internal/infra/smartlab"
)

// runDiary — портфель-дневник: повторяем сделки автора (Trendoman).
// Синонимы и правки разбора — data/diary/<канал>/{aliases,fixes}.json.
func runDiary(o Options, iss *moexiss.Client, out io.Writer) error {
	if len(o.Refs) != 1 {
		return fmt.Errorf("diary: укажите один канал (каталог HTML-экспорта или result.json)")
	}
	ref := o.Refs[0]
	since, err := time.Parse(time.DateOnly, o.Since)
	if err != nil {
		return fmt.Errorf("-since YYYY-MM-DD: %w", err)
	}
	info, msgs, err := ChannelSource(ref, o.Refresh, o.Offline).Messages()
	if err != nil {
		return err
	}
	dir := filepath.Join(dataDir, "diary", channelSlug(ref))
	aliases := map[string]string{}
	if err := readJSON(filepath.Join(dir, "aliases.json"), &aliases); err != nil {
		return err
	}
	var fixes []diary.Fix
	if err := readJSON(filepath.Join(dir, "fixes.json"), &fixes); err != nil {
		return err
	}
	universe, err := iss.Shares()
	if err != nil {
		return err
	}
	known := map[string]bool{}
	for _, s := range universe {
		known[s.SecID] = true
	}
	cs, ts, hs, ss, un := diary.Parse(msgs, since, known, aliases, fixes)
	fmt.Fprintf(out, "== %s · с %s · взносов %d, сделок %d, позиций автора для сверки %d\n", info.Title, o.Since, len(cs), len(ts), len(hs))
	if len(un) > 0 {
		fmt.Fprintf(out, "Не разобрано постов: %d — нужен синоним в %s/aliases.json или правка в fixes.json:\n", len(un), dir)
		for _, u := range un {
			fmt.Fprintf(out, "  пост %d %s: %s\n", u.PostID, u.PostedAt.Format(time.DateOnly), u.Reason)
		}
		return fmt.Errorf("разбор неполный")
	}

	ca, err := loadCorpActions()
	if err != nil {
		return err
	}
	bench, err := iss.IndexBars("MCFTR")
	if err != nil {
		return err
	}
	lqdt, err := iss.DailyBars("LQDT")
	if err != nil {
		return err
	}
	sl := smartlab.New(filepath.Join(dataDir, "cache", "smartlab"))
	sl.Offline = o.Offline
	fx := cbr.New(filepath.Join(dataDir, "cache", "cbr"))
	fx.Offline = o.Offline
	deps := diary.Deps{Resolver: iss, Shares: iss, Bonds: iss, Divs: sl, FX: fx, CA: ca, MoneyMarket: lqdt, Bench: bench}

	var results []diary.Result
	for _, mode := range []diary.Mode{diary.Author, diary.Follower} {
		r, err := diary.Run(cs, ts, hs, ss, diary.Params{Mode: mode, CommissionBps: o.CommissionBps, DebtSpread: o.DebtSpread}, deps)
		if err != nil {
			return err
		}
		results = append(results, r)
	}
	if probs := results[0].Problems; len(probs) > 0 {
		fmt.Fprintf(out, "\nПроблемы (модель автора), %d:\n", len(probs))
		for _, p := range probs {
			fmt.Fprintln(out, "  "+p)
		}
	}
	if probs := results[1].Problems; len(probs) > len(results[0].Problems) {
		fmt.Fprintf(out, "\nПроблемы только у последователя, %d:\n", len(probs)-len(results[0].Problems))
		seen := map[string]bool{}
		for _, p := range results[0].Problems {
			seen[p] = true
		}
		for _, p := range probs {
			if !seen[p] {
				fmt.Fprintln(out, "  "+p)
			}
		}
	}
	if w := results[0].Warnings; len(w) > 0 {
		fmt.Fprintf(out, "\nДопущения (модель автора), %d:\n", len(w))
		for _, x := range w {
			fmt.Fprintln(out, "  "+x)
		}
	}
	printChecks(out, results[0].Checks)
	printDiffs(out, results[0].Diffs)

	fmt.Fprintf(out, "\n%-14s %12s %12s %10s %9s %9s %10s %10s %10s %10s\n", "", "взносы", "капитал", "прибыль", "XIRR", "XIRR MCFTR", "купоны", "дивиденды", "проценты", "просадка")
	for i, name := range []string{"автор", "последователь"} {
		s := diary.Summarize(results[i])
		fmt.Fprintf(out, "%-14s %12.0f %12.0f %+10.0f %8.1f%% %9.1f%% %10.0f %10.0f %10.0f %9.1f%%\n", name,
			s.Contributed, s.Equity, s.Equity-s.Contributed, 100*s.XIRR, 100*s.BenchXIRR,
			s.ByKind["coupon"]+s.ByKind["amortization"]*0, s.ByKind["dividend"], s.ByKind["interest"], 100*s.MaxDrawdown)
		if i == 0 {
			fmt.Fprintf(out, "%-14s %12s %12.0f — те же взносы в те же дни в MCFTR (акции, полная доходность)\n", "", "", s.Bench)
			fmt.Fprintf(out, "%-14s %12s %12.0f — …в LQDT (денежный рынок, без риска): XIRR %.1f%%\n", "", "", s.MoneyMarket, 100*s.MMXIRR)
		}
	}
	printHoldings(out, results[0])

	// Журнал «как будто реальные деньги» — у последователя: так повторял бы подписчик.
	// Проблемы есть — прошлое недостоверно, журнал не трогаем.
	if len(results[1].Problems) > 0 {
		return fmt.Errorf("%d проблем — журнал не тронут", len(results[1].Problems))
	}
	cutoff := bench[len(bench)-1].Day
	ledger := ledgerfile.File{Path: filepath.Join(dataDir, "ledger", channelSlug(ref)+"-follower.jsonl")}
	old, err := ledger.Load()
	if err != nil {
		return err
	}
	fresh, err := portfolio.Reconcile(old, diary.LedgerOps(results[1], cutoff), cutoff)
	if err != nil {
		return err
	}
	printNewOps(out, fresh, ledger.Path, o.Dry)
	if o.Dry {
		return nil
	}
	return ledger.Append(fresh)
}

func printChecks(w io.Writer, cs []diary.Check) {
	if len(cs) == 0 {
		return
	}
	bad := 0
	for _, c := range cs {
		if math.Abs(c.Author-c.Our) > 0.5 {
			bad++
		}
	}
	fmt.Fprintf(w, "\nСверка с «Общая позиция N шт» автора: %d из %d сходятся\n", len(cs)-bad, len(cs))
	for _, c := range cs {
		if math.Abs(c.Author-c.Our) > 0.5 {
			fmt.Fprintf(w, "  пост %d %s %-14s у автора %g, у нас %g\n", c.PostID, c.Day.Format(time.DateOnly), c.SecID, c.Author, c.Our)
		}
	}
}

// printDiffs — сводка по бумагам: в скольких снимках #БалансПортфеля расходится и когда.
// Списки автора неполные (облигация Сегежи с его же $ISIN в них не появляется), поэтому
// «лишнее у нас» — не доказательство продажи; устойчивое многомесячное — почти наверняка она.
func printDiffs(w io.Writer, ds []diary.Diff) {
	if len(ds) == 0 {
		return
	}
	type span struct {
		n           int
		first, last time.Time
	}
	agg := func(get func(diary.Diff) []string) map[string]*span {
		m := map[string]*span{}
		for _, d := range ds {
			for _, c := range get(d) {
				if m[c] == nil {
					m[c] = &span{first: d.Day}
				}
				m[c].n++
				m[c].last = d.Day
			}
		}
		return m
	}
	show := func(title string, m map[string]*span) {
		keys := make([]string, 0, len(m))
		for k := range m {
			keys = append(keys, k)
		}
		sort.Slice(keys, func(i, j int) bool { return m[keys[i]].n > m[keys[j]].n })
		fmt.Fprintf(w, "  %s:", title)
		for _, k := range keys {
			fmt.Fprintf(w, " %s×%d (%s…%s)", k, m[k].n, m[k].first.Format("2006-01"), m[k].last.Format("2006-01"))
		}
		fmt.Fprintln(w)
	}
	fmt.Fprintf(w, "\nСостав против #БалансПортфеля (списки автора неполные): расхождения в %d снимках\n", len(ds))
	show("у автора есть, у нас нет", agg(func(d diary.Diff) []string { return d.Missing }))
	show("у нас есть, у автора нет", agg(func(d diary.Diff) []string { return d.Extra }))
}

func printHoldings(w io.Writer, r diary.Result) {
	type h struct {
		secid string
		qty   float64
	}
	var hs []h
	for k, v := range r.Holdings {
		hs = append(hs, h{k, v})
	}
	sort.Slice(hs, func(i, j int) bool { return hs[i].secid < hs[j].secid })
	fmt.Fprintf(w, "\nПозиции автора на конец (%d):", len(hs))
	for _, x := range hs {
		fmt.Fprintf(w, " %s:%g", x.secid, x.qty)
	}
	fmt.Fprintln(w)
}

func readJSON(path string, dst any) error {
	b, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	if err := json.Unmarshal(b, dst); err != nil {
		return fmt.Errorf("%s: %w", path, err)
	}
	return nil
}

var _ contract.InstrumentResolver = (*moexiss.Client)(nil)
