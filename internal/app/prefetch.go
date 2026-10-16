package app

import (
	"fmt"
	"io"
	"path/filepath"

	"tg-recs-audit/internal/infra/moexiss"
	"tg-recs-audit/internal/infra/smartlab"
)

// prefetchShares — бары ISS и выплаты smart-lab по всем акциям биржи в кэш: проверки по всему
// рынку (0014) читают кэш тем же парсером, что и команды.
func prefetchShares(o Options, iss *moexiss.Client, out io.Writer) error {
	universe, err := iss.Shares()
	if err != nil {
		return err
	}
	var ids []string
	for _, s := range universe {
		if s.Type == "common_share" || s.Type == "preferred_share" {
			ids = append(ids, s.SecID)
		}
	}
	if err := iss.Prefetch(ids); err != nil {
		return err
	}
	sl := smartlab.New(filepath.Join(dataDir, "cache", "smartlab"))
	sl.Offline = o.Offline
	rows := 0
	for _, id := range ids {
		r, err := sl.Dividends(id)
		if err != nil {
			fmt.Fprintf(out, "%s: %v\n", id, err)
			continue
		}
		rows += len(r)
	}
	fmt.Fprintf(out, "акций %d, выплат smart-lab %d\n", len(ids), rows)
	return nil
}
