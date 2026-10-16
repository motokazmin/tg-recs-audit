package diary

import (
	"encoding/json"
	"os"
	"strings"
	"testing"
	"time"

	"tg-recs-audit/internal/contract"
	"tg-recs-audit/internal/infra/tgexport"
)

// Разбор экспорта Trendoman (локальный, не в git). Пропускается без экспорта.
func TestРазборЭкспортаTrendoman(t *testing.T) {
	dir := "../../../data/exports/trendoman"
	if _, err := os.Stat(dir); err != nil {
		t.Skip("нет экспорта")
	}
	_, msgs, err := tgexport.HTMLSource{Dir: dir}.Messages()
	if err != nil {
		t.Fatal(err)
	}
	var u []contract.Security
	b, _ := os.ReadFile("../../../data/cache/iss/universe.json")
	_ = json.Unmarshal(b, &u)
	known := map[string]bool{}
	for _, s := range u {
		known[s.SecID] = true
	}
	aliases := map[string]string{}
	if b, err := os.ReadFile("../../../data/diary/trendoman/aliases.json"); err == nil {
		_ = json.Unmarshal(b, &aliases)
	}
	var fixes []Fix
	if b, err := os.ReadFile("../../../data/diary/trendoman/fixes.json"); err == nil {
		if err := json.Unmarshal(b, &fixes); err != nil {
			t.Fatal(err)
		}
	}
	cs, ts, hs, _, un := Parse(msgs, time.Date(2023, 2, 13, 0, 0, 0, 0, time.UTC), known, aliases, fixes)
	t.Logf("взносов %d, сделок %d, позиций для сверки %d, не разобрано постов %d", len(cs), len(ts), len(hs), len(un))
	if os.Getenv("SHOW") != "" {
		for _, u := range un {
			t.Logf("--- %d %s %s\n%s", u.PostID, u.PostedAt.Format("2006-01-02"), u.Reason, firstLines(u.Text, 8))
		}
		if os.Getenv("SHOW") == "all" {
			for _, x := range ts {
				t.Logf("%d %s %-4s %-14s %8g %10.2f | %s", x.PostID, x.PostedAt.Format("2006-01-02"), x.Side, x.Ref, x.Qty, x.Amount, x.Line)
			}
		}
	}
}

func firstLines(s string, n int) string {
	ls := strings.Split(s, "\n")
	if len(ls) > n {
		ls = ls[:n]
	}
	return strings.Join(ls, " | ")
}

func TestВзносыTrendoman(t *testing.T) {
	dir := "../../../data/exports/trendoman"
	if _, err := os.Stat(dir); err != nil {
		t.Skip("нет экспорта")
	}
	_, msgs, _ := tgexport.HTMLSource{Dir: dir}.Messages()
	cs, _, _, _, _ := Parse(msgs, time.Date(2023, 2, 13, 0, 0, 0, 0, time.UTC), map[string]bool{}, nil, nil)
	by := map[float64]int{}
	for _, c := range cs {
		by[c.Amount]++
		if c.Amount != 10000 {
			t.Logf("взнос %d %s: %.0f", c.PostID, c.PostedAt.Format("2006-01-02"), c.Amount)
		}
	}
	t.Logf("%v", by)
}
