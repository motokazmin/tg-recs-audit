package ideas

import (
	"encoding/json"
	"os"
	"sort"
	"testing"

	"tg-recs-audit/internal/contract"
)

// Разбор на снимке канала: доля отказов и их причины. Пропускается без снимка.
func TestРазборСнимкаКанала(t *testing.T) {
	b, err := os.ReadFile("../../../data/channels/ruforecasts.json")
	if err != nil {
		t.Skip("нет снимка канала")
	}
	var c struct{ Messages []contract.Message }
	if err := json.Unmarshal(b, &c); err != nil {
		t.Fatal(err)
	}
	var u []contract.Security
	if b, err := os.ReadFile("../../../data/cache/iss/universe.json"); err == nil {
		_ = json.Unmarshal(b, &u)
	}
	known := map[string]bool{}
	for _, s := range u {
		known[s.SecID] = true
	}
	got, rej := Parse(c.Messages, known)
	reasons := map[string]int{}
	for _, r := range rej {
		reasons[r.Reason]++
	}
	t.Logf("идей %d, отказов %d", len(got), len(rej))
	keys := make([]string, 0, len(reasons))
	for k := range reasons {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		t.Logf("  %4d %s", reasons[k], k)
	}
	if float64(len(rej)) > 0.02*float64(len(got)) {
		t.Errorf("отказов больше 2%%: шаблон канала поменялся?")
	}
}
