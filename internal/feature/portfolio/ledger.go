package portfolio

import (
	"math"
	"sort"

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

func round2(x float64) float64 { return math.Round(x*100) / 100 }
