package app

import (
	"encoding/csv"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"tg-recs-audit/internal/feature/divevents"
	"tg-recs-audit/internal/infra/moexiss"
	"tg-recs-audit/internal/infra/smartlab"
)

// runDivEvents — проверка 0010: покупка после рекомендации дивидендов, держать до отсечки.
func runDivEvents(o Options, iss *moexiss.Client, out io.Writer) error {
	if len(o.Refs) != 1 {
		return fmt.Errorf("divevents: укажите один канал")
	}
	info, msgs, err := ChannelSource(o.Refs[0], o.Refresh, o.Offline).Messages()
	if err != nil {
		return err
	}
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
	events, un := divevents.Parse(msgs, known)
	tickers := map[string]bool{}
	for _, e := range events {
		tickers[e.SecID] = true
	}
	if err := iss.Prefetch(sortedKeys(tickers)); err != nil {
		return err
	}
	mcftr, err := iss.IndexBars("MCFTR")
	if err != nil {
		return err
	}
	sl := smartlab.New(filepath.Join(dataDir, "cache", "smartlab"))
	sl.Offline = o.Offline
	var rs []divevents.Result
	skips := map[string]int{}
	unpaid := 0
	for _, e := range events {
		bars, err := iss.DailyBars(e.SecID)
		if err != nil {
			return err
		}
		paid, err := dividendPaid(sl, e)
		if err != nil {
			return err
		}
		r := divevents.Evaluate(e, bars, mcftr, paid)
		if !paid && r.Skip == "" {
			unpaid++
		}
		if r.Skip != "" {
			skips[r.Skip]++
		}
		rs = append(rs, r)
	}
	reasons := map[string]int{}
	for _, u := range un {
		k := u.Reason
		if i := strings.Index(k, ":"); i > 0 {
			k = k[:i]
		}
		reasons[k]++
	}
	fmt.Fprintf(out, "== %s · рекомендации дивидендов (0010)\nСобытий %d. Не разобрано:", info.Title, len(events))
	for _, k := range sortedKeys(reasons) {
		fmt.Fprintf(out, " %s %d;", k, reasons[k])
	}
	fmt.Fprintf(out, "\nВыплата не подтверждена smart-lab (дивиденд = 0): %d", unpaid)
	fmt.Fprint(out, "\nНе исполнимо:")
	for _, k := range sortedKeys(skips) {
		fmt.Fprintf(out, " %s %d;", k, skips[k])
	}
	fmt.Fprintln(out)

	if err := writeDivEvents(filepath.Join(dataDir, "analysis", "divevents-"+channelSlug(o.Refs[0])+".csv"), rs); err != nil {
		return err
	}

	exB := func(r divevents.Result) float64 { return r.RetB - r.MCB }
	exA := func(r divevents.Result) float64 { return r.RetA - r.MCA }
	sel := func(keep func(divevents.Result) bool) []divevents.Result {
		var x []divevents.Result
		for _, r := range rs {
			if keep(r) {
				x = append(x, r)
			}
		}
		return x
	}
	fmt.Fprintf(out, "\n%-36s %5s %5s %9s %9s %7s %7s\n", "превышение над MCFTR", "N", "нед.", "среднее", "медиана", "в плюсе", "t нед.")
	row := func(name string, x []divevents.Result, f func(divevents.Result) float64) divevents.Stat {
		s := divevents.Summarize(x, f)
		if s.N > 0 {
			fmt.Fprintf(out, "%-36s %5d %5d %+8.2f%% %+8.2f%% %6.0f%% %+7.2f\n", name, s.N, s.Weeks, 100*s.Mean, 100*s.Median, 100*s.Pos, s.TWeeks)
		}
		return s
	}
	split := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)
	main := row("B: через отсечку (+ дивиденд)", rs, exB)
	h1 := row("  события до 2024", sel(func(r divevents.Result) bool { return r.EntryDay.Before(split) }), exB)
	h2 := row("  события с 2024", sel(func(r divevents.Result) bool { return !r.EntryDay.Before(split) }), exB)
	yt := tercile(sel(func(r divevents.Result) bool { return r.Skip == "" }), func(r divevents.Result) float64 { return r.Yield })
	var ys []divevents.Stat
	for i, name := range []string{"доходность: нижняя треть", "доходность: средняя", "доходность: верхняя"} {
		ys = append(ys, row("  "+name, yt[i], exB))
	}
	fmt.Fprintln(out, "\nРазрезы (описательно):")
	row("B после НДФЛ 13% с дивиденда", rs, func(r divevents.Result) float64 { return r.RetB - r.Tax - r.MCB })
	row("A: разгон до T-1, без дивиденда", rs, exA)
	dt := tercile(sel(func(r divevents.Result) bool { return r.Skip == "" }), func(r divevents.Result) float64 { return float64(r.Days) })
	for i, name := range []string{"до T-1 мало дней", "средне", "много дней"} {
		row("  B, "+name, dt[i], exB)
	}
	row("  B, промежуточные", sel(func(r divevents.Result) bool { return r.Interim }), exB)
	row("  B, годовые", sel(func(r divevents.Result) bool { return !r.Interim }), exB)

	ok := func(b bool) string {
		if b {
			return "да"
		}
		return "НЕТ"
	}
	same := 0
	for _, s := range ys {
		if s.N > 0 && (s.Mean > 0) == (main.Mean > 0) {
			same++
		}
	}
	var ex, days []float64
	for _, r := range rs {
		if r.Skip == "" {
			ex = append(ex, exB(r))
			days = append(days, float64(r.Days))
		}
	}
	sort.Float64s(ex)
	sort.Float64s(days)
	q := func(x []float64, p float64) float64 { return x[int(p*float64(len(x)-1))] }
	fmt.Fprintf(out, "\nПревышение B, перцентили 5/25/50/75/95: %+.1f%% %+.1f%% %+.1f%% %+.1f%% %+.1f%%; торговых дней до T-1: медиана %.0f (10%%: %.0f, 90%%: %.0f)\n",
		100*q(ex, .05), 100*q(ex, .25), 100*q(ex, .5), 100*q(ex, .75), 100*q(ex, .95), q(days, .5), q(days, .1), q(days, .9))
	fmt.Fprintln(out, "\nКритерии 0010 (выход B):")
	fmt.Fprintf(out, "  1. среднее и медиана > 0: %s\n", ok(main.Mean > 0 && main.Median > 0))
	fmt.Fprintf(out, "  2. t по неделям > 2: %s (%.2f)\n", ok(main.TWeeks > 2), main.TWeeks)
	fmt.Fprintf(out, "  3. тот же знак в половинах: %s\n", ok((h1.Mean > 0) == (main.Mean > 0) && (h2.Mean > 0) == (main.Mean > 0)))
	fmt.Fprintf(out, "  4. тот же знак в ≥ 2 из 3 терцилей доходности: %s (%d)\n", ok(same >= 2), same)
	return nil
}

// dividendPaid — есть ли выплата с T-1 в пределах недели от T-1 из поста (сумма не сверяется:
// у источника она пересчитана под поздние сплиты, ADR 0003). Префы — на странице обыкновенных.
func dividendPaid(sl *smartlab.Client, e divevents.Event) (bool, error) {
	rows, err := sl.Dividends(e.SecID)
	if err != nil {
		return false, err
	}
	for _, r := range rows {
		d, err := time.Parse(time.DateOnly, r.LastBuyDay)
		if err != nil {
			continue
		}
		if diff := d.Sub(e.LastBuy).Hours() / 24; diff >= -7 && diff <= 7 {
			return true, nil
		}
	}
	return false, nil
}

// writeDivEvents — события для проверок по всему рынку (0014): какие выплаты канал объявил.
func writeDivEvents(path string, rs []divevents.Result) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	defer f.Close()
	w := csv.NewWriter(f)
	_ = w.Write([]string{"secid", "post_id", "posted_at", "last_buy", "div", "entry_day", "ret_b", "mcftr_b", "paid", "skip"})
	for _, r := range rs {
		entry := ""
		if !r.EntryDay.IsZero() {
			entry = r.EntryDay.Format(time.DateOnly)
		}
		_ = w.Write([]string{r.SecID, strconv.FormatInt(r.PostID, 10), r.PostedAt.Format(time.RFC3339), r.LastBuy.Format(time.DateOnly),
			strconv.FormatFloat(r.Div, 'g', -1, 64), entry, strconv.FormatFloat(r.RetB, 'g', 6, 64), strconv.FormatFloat(r.MCB, 'g', 6, 64),
			strconv.FormatBool(r.Paid), r.Skip})
	}
	w.Flush()
	return w.Error()
}

func tercile(x []divevents.Result, key func(divevents.Result) float64) [3][]divevents.Result {
	sort.Slice(x, func(i, j int) bool { return key(x[i]) < key(x[j]) })
	var out [3][]divevents.Result
	for i, r := range x {
		out[min(2, i*3/len(x))] = append(out[min(2, i*3/len(x))], r)
	}
	return out
}
