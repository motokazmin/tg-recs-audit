package app

import (
	"encoding/csv"
	"fmt"
	"io"
	"log"
	"math"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"sync"
	"time"

	"tg-recs-audit/internal/feature/placements"
	"tg-recs-audit/internal/infra/moexiss"
)

// runPlacements — проверка 0006: размещения облигаций по номиналу против LQDT.
func runPlacements(o Options, iss *moexiss.Client, out io.Writer) error {
	all, err := iss.Bonds()
	if err != nil {
		return err
	}
	// Карточки — тысячи запросов: параллельно, ISS терпит несколько потоков.
	var ids []string
	for _, s := range all {
		if (s.Type == "corporate_bond" || s.Type == "exchange_bond") && s.SecID >= "RU000A105" && s.SecID < "RU000B" {
			ids = append(ids, s.SecID)
		}
	}
	if err := parallel(ids, 6, "карточки", func(id string) error { _, err := iss.Card(id); return err }); err != nil {
		return err
	}
	from, to := time.Date(2023, 1, 1, 0, 0, 0, 0, time.UTC), time.Date(2026, 7, 15, 0, 0, 0, 0, time.UTC)
	bonds, skip, err := placements.Universe(all, iss.Card, from, to)
	if err != nil {
		return err
	}
	lqdt, err := iss.DailyBars("LQDT")
	if err != nil {
		return err
	}
	ns := []int{5, 20, 60}
	var mu sync.Mutex
	var obs []placements.Obs
	byID := map[string]placements.Bond{}
	for _, b := range bonds {
		byID[b.SecID] = b
	}
	var bids []string
	for _, b := range bonds {
		bids = append(bids, b.SecID)
	}
	err = parallel(bids, 6, "окна истории", func(id string) error {
		res, err := placements.Evaluate(byID[id], iss, lqdt, ns)
		if err != nil {
			return err
		}
		mu.Lock()
		obs = append(obs, res...)
		mu.Unlock()
		return nil
	})
	if err != nil {
		return err
	}
	sort.Slice(obs, func(i, j int) bool {
		if !obs[i].Issue.Equal(obs[j].Issue) {
			return obs[i].Issue.Before(obs[j].Issue)
		}
		return obs[i].SecID < obs[j].SecID
	})
	if err := writeObs(filepath.Join(dataDir, "analysis", "placements.csv"), obs); err != nil {
		return err
	}
	printPlacements(out, bonds, skip, obs)
	return nil
}

func printPlacements(w io.Writer, bonds []placements.Bond, skip map[string]int, obs []placements.Obs) {
	fmt.Fprintf(w, "== Размещения по номиналу против LQDT (регистрация 0006)\nВ выборке выпусков: %d. Отсеяно:", len(bonds))
	for _, k := range sortedKeys(skip) {
		fmt.Fprintf(w, " %s %d;", k, skip[k])
	}
	fmt.Fprintln(w)
	at := func(n int, keep func(placements.Obs) bool) []placements.Obs {
		var r []placements.Obs
		for _, o := range obs {
			if o.N == n && (keep == nil || keep(o)) {
				r = append(r, o)
			}
		}
		return r
	}
	none, notPar := 0, 0
	for _, o := range at(20, nil) {
		switch {
		case o.NotPar != 0:
			notPar++
		case o.NoTrades:
			none++
		}
	}
	fmt.Fprintf(w, "Не по номиналу (цена 1-го дня вне %.0f–%.0f%%): %d; без сделок за 20 дней: %d — в средние не входят\n\n", 97.0, 103.0, notPar, none)
	fmt.Fprintf(w, "%-32s %5s %5s %9s %9s %8s %8s %8s %7s\n", "", "N", "нед.", "доход", "LQDT", "превыш.", "медиана", "в плюсе", "t нед.")
	row := func(name string, os []placements.Obs) placements.Stat {
		s := placements.Summarize(os)
		if s.N > 0 {
			fmt.Fprintf(w, "%-32s %5d %5d %+8.2f%% %+8.2f%% %+7.2f%% %+7.2f%% %7.0f%% %+7.2f\n", name, s.N, s.Weeks,
				100*s.MeanRet, 100*s.MeanMM, 100*s.Mean, 100*s.Median, 100*s.Positive, s.TWeeks)
		}
		return s
	}
	main := row("N=20, все", at(20, nil))
	h1 := row("  размещения 2023–2024", at(20, func(o placements.Obs) bool { return o.Issue.Year() <= 2024 }))
	h2 := row("  размещения 2025–2026", at(20, func(o placements.Obs) bool { return o.Issue.Year() >= 2025 }))
	phases := map[string]placements.Stat{}
	for _, ph := range []string{"растёт", "стоит", "падает"} {
		phases[ph] = row("  ставка "+ph, at(20, func(o placements.Obs) bool { return o.Phase == ph }))
	}
	row("N=5, все", at(5, nil))
	row("N=60, все", at(60, nil))

	fmt.Fprintln(w, "\nРазрезы N=20 (описательно, не для выбора):")
	valid := func(o placements.Obs) bool { return !o.NoTrades && o.NotPar == 0 }
	sp := terciles(at(20, func(o placements.Obs) bool { return !math.IsNaN(o.Spread) && valid(o) }), func(o placements.Obs) float64 { return o.Spread })
	for i, name := range []string{"купон−LQDT: нижняя треть", "купон−LQDT: средняя", "купон−LQDT: верхняя"} {
		row("  "+name, sp[i])
	}
	sz := terciles(at(20, valid), func(o placements.Obs) float64 { return o.IssueSize })
	for i, name := range []string{"объём: нижняя треть", "объём: средняя", "объём: верхняя"} {
		row("  "+name, sz[i])
	}
	row("  для квалов", at(20, func(o placements.Obs) bool { return o.Qual }))
	row("  для всех", at(20, func(o placements.Obs) bool { return !o.Qual }))

	ok := func(b bool) string {
		if b {
			return "да"
		}
		return "НЕТ"
	}
	sameSign := func(ss ...placements.Stat) bool {
		for _, s := range ss {
			if s.N > 0 && (s.Mean > 0) != (main.Mean > 0) {
				return false
			}
		}
		return true
	}
	fmt.Fprintln(w, "\nКритерии 0006 (N=20):")
	fmt.Fprintf(w, "  1. среднее и медиана превышения > 0: %s\n", ok(main.Mean > 0 && main.Median > 0))
	fmt.Fprintf(w, "  2. t по неделям > 2: %s (%.2f)\n", ok(main.TWeeks > 2), main.TWeeks)
	fmt.Fprintf(w, "  3. тот же знак в обеих половинах: %s\n", ok(sameSign(h1, h2)))
	fmt.Fprintf(w, "  4. тот же знак во всех фазах ставки: %s\n", ok(sameSign(phases["растёт"], phases["стоит"], phases["падает"])))
}

func terciles(os []placements.Obs, key func(placements.Obs) float64) [3][]placements.Obs {
	sort.Slice(os, func(i, j int) bool { return key(os[i]) < key(os[j]) })
	var out [3][]placements.Obs
	for i, o := range os {
		out[min(2, i*3/len(os))] = append(out[min(2, i*3/len(os))], o)
	}
	return out
}

func writeObs(path string, obs []placements.Obs) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	defer f.Close()
	w := csv.NewWriter(f)
	_ = w.Write([]string{"secid", "name", "issue", "coupon", "qual", "issue_size", "n", "ret", "lqdt", "excess", "phase", "spread", "no_trades", "not_par"})
	ff := func(x float64) string { return strconv.FormatFloat(x, 'f', 6, 64) }
	for _, o := range obs {
		_ = w.Write([]string{o.SecID, o.Name, o.Issue.Format(time.DateOnly), ff(o.Coupon), strconv.FormatBool(o.Qual), ff(o.IssueSize),
			strconv.Itoa(o.N), ff(o.Ret), ff(o.MM), ff(o.Excess()), o.Phase, ff(o.Spread), strconv.FormatBool(o.NoTrades), ff(o.NotPar)})
	}
	w.Flush()
	return w.Error()
}

// parallel — f по каждому id в k потоков, с прогрессом в лог; первая ошибка останавливает.
func parallel(ids []string, k int, what string, f func(string) error) error {
	var wg sync.WaitGroup
	var mu sync.Mutex
	var first error
	done := 0
	ch := make(chan string)
	for i := 0; i < k; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for id := range ch {
				err := f(id)
				mu.Lock()
				if err != nil && first == nil {
					first = fmt.Errorf("%s %s: %w", what, id, err)
				}
				done++
				if done%500 == 0 {
					log.Printf("%s: %d из %d", what, done, len(ids))
				}
				mu.Unlock()
			}
		}()
	}
	for _, id := range ids {
		ch <- id
	}
	close(ch)
	wg.Wait()
	return first
}
