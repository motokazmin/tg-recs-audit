package app

import (
	"encoding/csv"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"sync"
	"time"

	"tg-recs-audit/internal/contract"
	"tg-recs-audit/internal/feature/signals"
	"tg-recs-audit/internal/infra/moexiss"
)

// runSignals — проверка 0008: карточки сигналов по фьючерсам, исполненные правилом.
func runSignals(o Options, iss *moexiss.Client, out io.Writer) error {
	if len(o.Refs) != 1 {
		return fmt.Errorf("signals: укажите один канал")
	}
	info, msgs, err := ChannelSource(o.Refs[0], o.Refresh, o.Offline).Messages()
	if err != nil {
		return err
	}
	sigs, un := signals.Parse(msgs)
	sort.SliceStable(sigs, func(i, j int) bool { return sigs[i].PostedAt.Before(sigs[j].PostedAt) })

	// Новая карточка по тому же инструменту снимает неисполненный ордер.
	expire := make([]time.Time, len(sigs))
	next := map[string]time.Time{}
	for i := len(sigs) - 1; i >= 0; i-- {
		expire[i] = next[sigs[i].Asset]
		next[sigs[i].Asset] = sigs[i].PostedAt
	}

	// Свечи одного дня нужны многим сигналам: замок на (контракт, день), чтобы не писать файл кэша дважды.
	var locks sync.Map
	bars := func(secid string, day time.Time) ([]contract.Bar, error) {
		k := secid + day.Format(time.DateOnly)
		mu, _ := locks.LoadOrStore(k, &sync.Mutex{})
		mu.(*sync.Mutex).Lock()
		defer mu.(*sync.Mutex).Unlock()
		return iss.FuturesBars(secid, day)
	}
	now := time.Now().In(time.FixedZone("MSK", 3*3600))
	asOf := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.UTC)

	main := make([]signals.Outcome, len(sigs))
	diag := make([]signals.Outcome, len(sigs))
	ids := make([]string, len(sigs))
	for i := range sigs {
		ids[i] = strconv.Itoa(i)
	}
	err = parallel(ids, 6, "сигналы", func(id string) error {
		i, _ := strconv.Atoi(id)
		var err error
		if main[i], err = signals.Simulate(sigs[i], bars, expire[i], false, asOf); err != nil {
			return err
		}
		if sigs[i].Kind != signals.Market {
			diag[i], err = signals.Simulate(sigs[i], bars, expire[i], true, asOf)
		}
		return err
	})
	if err != nil {
		return err
	}
	if err := writeOutcomes(filepath.Join(dataDir, "analysis", "signals-"+channelSlug(o.Refs[0])+".csv"), main); err != nil {
		return err
	}
	printSignals(out, info.Title, len(msgs), un, main, diag)
	return nil
}

var groups = map[string]string{
	"BR": "сырьё", "BRENT": "сырьё", "NG": "сырьё", "GOLD": "сырьё", "SILV": "сырьё", "PLT": "сырьё",
	"RTS": "индексы и валюта", "MIX": "индексы и валюта", "MXI": "индексы и валюта", "SI": "индексы и валюта",
	"CNY": "индексы и валюта", "ED": "индексы и валюта", "EU": "индексы и валюта", "SPYF": "индексы и валюта",
	"SBRF": "фьючерсы на акции", "GAZR": "фьючерсы на акции", "VTBR": "фьючерсы на акции", "LKOH": "фьючерсы на акции",
	"MGNT": "фьючерсы на акции", "ROSN": "фьючерсы на акции", "SBER": "фьючерсы на акции",
}

func printSignals(w io.Writer, title string, posts int, un []signals.Unparsed, main, diag []signals.Outcome) {
	status := map[string]int{}
	for _, o := range main {
		status[o.Status]++
	}
	fmt.Fprintf(w, "== %s · сигналы по правилу 0008\nПостов %d, карточек %d, не разобрано %d. Исходы:", title, posts, len(main)+len(un), len(un))
	for _, k := range sortedKeys(status) {
		fmt.Fprintf(w, " %s %d;", k, status[k])
	}
	fmt.Fprintln(w)
	reasons := map[string]int{}
	for _, u := range un {
		reasons[u.Reason]++
	}
	for _, k := range sortedKeys(reasons) {
		if reasons[k] > 2 {
			fmt.Fprintf(w, "  не разобрано: %-30s %d\n", k, reasons[k])
		}
	}
	sel := func(os []signals.Outcome, keep func(signals.Outcome) bool) []signals.Outcome {
		var r []signals.Outcome
		for _, o := range os {
			if keep == nil || keep(o) {
				r = append(r, o)
			}
		}
		return r
	}
	fmt.Fprintf(w, "\n%-34s %6s %5s %8s %8s %6s %7s\n", "", "сделок", "нед.", "средн. R", "медиана", "win", "t нед.")
	row := func(name string, os []signals.Outcome) signals.Stat {
		s := signals.Summarize(os)
		if s.N > 0 {
			fmt.Fprintf(w, "%-34s %6d %5d %+8.3f %+8.3f %5.0f%% %+7.2f\n", name, s.N, s.Weeks, s.Mean, s.Median, 100*s.Win, s.TWeeks)
		}
		return s
	}
	all := row("все исполненные", main)
	h1 := row("  карточки 2019–2022", sel(main, func(o signals.Outcome) bool { return o.PostedAt.Year() <= 2022 }))
	h2 := row("  карточки 2023–2026", sel(main, func(o signals.Outcome) bool { return o.PostedAt.Year() >= 2023 }))
	gs := map[string]signals.Stat{}
	for _, g := range []string{"сырьё", "индексы и валюта", "фьючерсы на акции"} {
		gs[g] = row("  "+g, sel(main, func(o signals.Outcome) bool { return groups[o.Asset] == g }))
	}
	fmt.Fprintln(w, "\nРазрезы (описательно):")
	for _, k := range []signals.Kind{signals.Limit, signals.Stop, signals.Market} {
		row("  ордер "+string(k), sel(main, func(o signals.Outcome) bool { return o.Kind == k }))
	}
	row("  тейк автора", sel(main, func(o signals.Outcome) bool { return o.Take != 0 }))
	row("  тейк открытый → 2R", sel(main, func(o signals.Outcome) bool { return o.Take == 0 }))
	for _, why := range []string{"stop", "take", "time", "contract"} {
		row("  выход "+why, sel(main, func(o signals.Outcome) bool { return o.Reason == why }))
	}
	assets := map[string]bool{}
	for _, o := range main {
		assets[o.Asset] = true
	}
	for _, a := range sortedKeys(assets) {
		row("  "+a, sel(main, func(o signals.Outcome) bool { return o.Asset == a }))
	}
	fmt.Fprintln(w, "\nДиагностика: те же стоп и тейк, вход по рынку в момент поста вместо ордера автора")
	row("  ордер автора (limit/stop)", sel(main, func(o signals.Outcome) bool { return o.Kind != signals.Market }))
	row("  вход по рынку", sel(diag, func(o signals.Outcome) bool { return o.Kind != signals.Market }))

	ok := func(b bool) string {
		if b {
			return "да"
		}
		return "НЕТ"
	}
	pos := 0
	for _, g := range gs {
		if g.N > 0 && g.Mean > 0 {
			pos++
		}
	}
	fmt.Fprintln(w, "\nКритерии 0008:")
	fmt.Fprintf(w, "  1. средний R > +0.05 и медиана > 0: %s\n", ok(all.Mean > 0.05 && all.Median > 0))
	fmt.Fprintf(w, "  2. t по неделям > 2: %s (%.2f)\n", ok(all.TWeeks > 2), all.TWeeks)
	fmt.Fprintf(w, "  3. тот же знак в половинах: %s\n", ok((h1.Mean > 0) == (all.Mean > 0) && (h2.Mean > 0) == (all.Mean > 0)))
	fmt.Fprintf(w, "  4. плюс в ≥ 2 из 3 групп: %s (%d)\n", ok(pos >= 2), pos)
}

func writeOutcomes(path string, os_ []signals.Outcome) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	defer f.Close()
	w := csv.NewWriter(f)
	_ = w.Write([]string{"post_id", "posted_at", "asset", "secid", "long", "kind", "price", "stop", "take", "status", "entry", "exit", "reason", "r", "exit_at"})
	ff := func(x float64) string { return strconv.FormatFloat(x, 'f', -1, 64) }
	for _, o := range os_ {
		_ = w.Write([]string{strconv.FormatInt(o.PostID, 10), o.PostedAt.Format(time.DateTime), o.Asset, o.SecID, strconv.FormatBool(o.Long),
			string(o.Kind), ff(o.Price), ff(o.Stop), ff(o.Take), o.Status, ff(o.Entry), ff(o.Exit), o.Reason, ff(o.R), o.At.Format(time.DateTime)})
	}
	w.Flush()
	return w.Error()
}
