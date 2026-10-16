package app

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"os"
	"path/filepath"
	"time"

	"tg-recs-audit/internal/feature/houses"
	"tg-recs-audit/internal/feature/ideas"
	"tg-recs-audit/internal/feature/portfolio"
	"tg-recs-audit/internal/infra/moexiss"
)

var aliasesPath = filepath.Join(dataDir, "analyst_aliases.json")

func runHouses(o Options, iss *moexiss.Client, out io.Writer) error {
	p, err := prepare(o, iss)
	if err != nil {
		return err
	}
	aliases, err := loadAliases()
	if err != nil {
		return err
	}
	split, err := time.Parse(time.DateOnly, o.Split)
	if err != nil {
		return fmt.Errorf("-split YYYY-MM-DD: %w", err)
	}
	house := func(a string) string {
		if h, ok := aliases[a]; ok {
			return h
		}
		if a == "" {
			return "(не указан)"
		}
		return a
	}
	var problems []string
	run := func(ii []ideas.Idea) ([]portfolio.Position, error) {
		res, err := portfolio.Run(ii, iss, p.divs, p.bench, p.ca, p.params, p.asOf)
		problems = append(problems, res.Problems...)
		return res.Positions, err
	}
	rep, err := houses.Compare(p.ideas, house, run, split, o.MinIdeas)
	if err != nil {
		return err
	}
	if len(problems) > 0 {
		return problemsErr(out, problems)
	}

	fmt.Fprintf(out, "== %s · по домам · горизонт %s · stack=%v · домов с ≥%d идеями: %d\n",
		p.info.Title, p.params.Horizon, p.params.Stack, o.MinIdeas, len(rep.Rows))
	fmt.Fprintf(out, "Превышение над %s на позицию; t — его t-статистика (|t|<2 — от нуля не отличить)\n\n", p.bname)
	fmt.Fprintf(out, "%-22s | %25s | %25s | %25s\n", "", "весь период", "до "+o.Split, "с "+o.Split)
	fmt.Fprintf(out, "%-22s | %4s %6s %6s %6s | %4s %6s %6s %6s | %4s %6s %6s %6s\n", "дом",
		"N", "win%", "vsИнд", "t", "N", "win%", "vsИнд", "t", "N", "win%", "vsИнд", "t")
	for _, r := range rep.Rows {
		fmt.Fprintf(out, "%-22s | %s | %s | %s\n", trunc(r.House, 22), cell(r.All), cell(r.In), cell(r.Out))
	}
	fmt.Fprintf(out, "\nРанговая корреляция превышения «до» и «после» (Спирмен) по %d домам с ≥%d позициями в обоих периодах: %s\n",
		rep.RankedN, o.MinIdeas, fmtCorr(rep.RankCorr))
	fmt.Fprintln(out, "Около нуля — рейтинг первого периода не предсказывает второй: выбор дома по истории не работает.")
	return nil
}

func cell(s houses.Stat) string {
	if s.N == 0 {
		return fmt.Sprintf("%4s %6s %6s %6s", "0", "", "", "")
	}
	return fmt.Sprintf("%4d %5.0f%% %+5.1f%% %+6.2f", s.N, 100*s.WinRate, 100*s.MeanExcess, s.T)
}

func fmtCorr(x float64) string {
	if math.IsNaN(x) {
		return "— (мало домов)"
	}
	return fmt.Sprintf("%+.2f", x)
}

func trunc(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n-1]) + "…"
}

func loadAliases() (map[string]string, error) {
	m := map[string]string{}
	b, err := os.ReadFile(aliasesPath)
	if errors.Is(err, os.ErrNotExist) {
		return m, nil
	}
	if err != nil {
		return nil, err
	}
	if err := json.Unmarshal(b, &m); err != nil {
		return nil, fmt.Errorf("%s: %w", aliasesPath, err)
	}
	delete(m, "_comment")
	return m, nil
}
