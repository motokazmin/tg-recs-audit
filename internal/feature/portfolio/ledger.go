package portfolio

import (
	"fmt"
	"math"
	"sort"
	"time"

	"tg-recs-audit/internal/contract"
)

// Ops раскладывает прогон в операции счёта по дням. Открытая позиция — только покупка.
func Ops(r Result) []contract.Op {
	var ops []contract.Op
	for _, p := range r.Positions {
		base := contract.Op{SecID: p.Idea.SecID, PostID: p.Idea.PostID, PostedAt: p.Idea.PostedAt, Target: p.Idea.Target, Analyst: p.Idea.Analyst}
		buy := base
		buy.Kind, buy.Day, buy.Qty, buy.Price, buy.Amount = "buy", key(p.EntryDay), p.Qty, p.EntryPrice, round2(p.Cost)
		ops = append(ops, buy)
		for _, d := range p.Dividends {
			div := base
			div.Kind, div.Day, div.SecID, div.Qty, div.Price, div.Amount = "dividend", key(d.LastBuyDay), d.SecID, d.Qty, d.PerShare, round2(d.Amount())
			ops = append(ops, div)
		}
		if !p.Open() {
			sell := base
			sell.Kind, sell.Day, sell.SecID, sell.Qty, sell.Price, sell.Amount, sell.Reason =
				"sell", key(p.ExitDay), p.SecID, p.ExitQty, p.ExitPrice, round2(p.Proceeds), p.ExitReason
			ops = append(ops, sell)
		}
	}
	for _, s := range r.Skips {
		ops = append(ops, contract.Op{
			Kind: "skip", Day: key(s.Day), SecID: s.Idea.SecID, PostID: s.Idea.PostID, PostedAt: s.Idea.PostedAt,
			Target: s.Idea.Target, Analyst: s.Idea.Analyst, Reason: s.Reason,
		})
	}
	rank := map[string]int{"dividend": 0, "sell": 1, "buy": 2, "skip": 3}
	sort.SliceStable(ops, func(i, j int) bool {
		a, b := ops[i], ops[j]
		if a.Day != b.Day {
			return a.Day < b.Day
		}
		if rank[a.Kind] != rank[b.Kind] {
			return rank[a.Kind] < rank[b.Kind]
		}
		return a.PostID < b.PostID
	})
	for i := range ops {
		ops[i].Seq = i + 1
	}
	return ops
}

// Reconcile сверяет журнал с пересчётом и возвращает, что дописать.
//
// В журнал попадают только операции дней раньше cutoff (сегодня по Москве): день,
// который ещё торгуется, может измениться. Записанные операции обязаны совпасть
// с пересчётом один в один — иначе прошлое счёта поменялось (правка кода, правил,
// таблицы корпоративных действий), и это решение человека, а не тихая перезапись.
func Reconcile(ledger, computed []contract.Op, cutoff time.Time) ([]contract.Op, error) {
	var settled []contract.Op
	for _, op := range computed {
		if op.Day < key(cutoff) {
			settled = append(settled, op)
		}
	}
	if len(ledger) > len(settled) {
		return nil, fmt.Errorf("в журнале %d операций, а пересчёт даёт %d до %s: прошлое изменилось", len(ledger), len(settled), key(cutoff))
	}
	for i, old := range ledger {
		if !same(old, settled[i]) {
			return nil, fmt.Errorf("операция #%d в журнале не совпадает с пересчётом:\n  журнал:   %s\n  пересчёт: %s\nпрошлое счёта изменилось — разберитесь и заведите новый журнал", old.Seq, show(old), show(settled[i]))
		}
	}
	return settled[len(ledger):], nil
}

func same(a, b contract.Op) bool {
	return a.Seq == b.Seq && a.Kind == b.Kind && a.Day == b.Day && a.SecID == b.SecID && a.PostID == b.PostID &&
		a.Reason == b.Reason && near(a.Qty, b.Qty) && near(a.Price, b.Price) && near(a.Amount, b.Amount)
}

func near(a, b float64) bool { return math.Abs(a-b) <= 1e-6*math.Max(1, math.Abs(a)) }

func show(o contract.Op) string {
	return fmt.Sprintf("%s %s %s пост %d qty %.4g по %.4g = %.2f %s", o.Day, o.Kind, o.SecID, o.PostID, o.Qty, o.Price, o.Amount, o.Reason)
}

func round2(x float64) float64 { return math.Round(x*100) / 100 }
