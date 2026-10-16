package paperstrat

import (
	"math"
	"sort"
	"sync"
	"time"

	"tg-recs-audit/internal/contract"
	"tg-recs-audit/internal/feature/placements"
	"tg-recs-audit/internal/paper"
)

// Placements — B и C из 0013: заявка на размещение по номиналу, аллокация по модели 0012,
// выход через 20 торговых дней; FallingOnly — только при снижении 20-дневной доходности LQDT.
type Placements struct {
	Slug, Heading string
	StartDay      time.Time
	Cap, PerTrade float64 // 1 000 000; 20 000 на заявку
	FallingOnly   bool
	Universe      func() ([]contract.Security, error)
	Card          func(string) (map[string]string, error)
	Bonds         contract.BondData
	MM            func() ([]contract.Bar, error) // LQDT: календарь и фильтр ставки
	cache         map[string][]contract.BondBar  // только для Ops
	mmu           sync.Mutex                     // marks — для Mark (админка, пока идёт пересчёт)
	marks         map[string][]contract.BondBar
	marksAt       time.Time
}

const holdDays = 20

func (p *Placements) Name() string      { return p.Slug }
func (p *Placements) Title() string     { return p.Heading }
func (p *Placements) Benchmark() string { return "LQDT" }
func (p *Placements) Capital() float64  { return p.Cap }
func (p *Placements) Start() time.Time  { return p.StartDay }

// allocation — сценарий 2 из 0012 по close первого дня торгов.
func allocation(close1 float64) float64 {
	switch {
	case close1 <= 100.5:
		return 1
	case close1 <= 102:
		return 0.4
	}
	return 0.15
}

func (p *Placements) Ops(asOf time.Time, _ paper.Memory) ([]contract.Op, error) {
	p.cache = map[string][]contract.BondBar{}
	all, err := p.Universe()
	if err != nil {
		return nil, err
	}
	bonds, _, err := placements.Universe(all, p.Card, p.StartDay, asOf.AddDate(0, 0, -1))
	if err != nil {
		return nil, err
	}
	mm, err := p.MM()
	if err != nil {
		return nil, err
	}
	cal := mm[:sort.Search(len(mm), func(i int) bool { return !mm[i].Day.Before(asOf) })]

	type action struct {
		day   time.Time
		order int
		op    contract.Op
		kind  string
		qty   float64
		px    float64
		id    string
	}
	var acts []action
	for _, b := range bonds {
		bars, err := p.bars(b.SecID, asOf)
		if err != nil {
			return nil, err
		}
		if len(bars) == 0 {
			continue // торги ещё не начались
		}
		first := bars[0]
		base := contract.Op{Day: first.Day.Format(time.DateOnly), SecID: b.SecID, Reason: b.Name}
		px := first.Open
		if px <= 0 {
			px = first.Close
		}
		if px < 97 || px > 103 {
			base.Kind, base.Reason = "skip", "размещение не по номиналу"
			acts = append(acts, action{day: first.Day, order: 2, op: base, kind: "skip"})
			continue
		}
		i0 := sort.Search(len(cal), func(i int) bool { return !cal[i].Day.Before(first.Day) })
		if i0 >= len(cal) {
			continue
		}
		if p.FallingOnly && (i0 < 20 || placements.Yield20(cal, i0) >= placements.Yield20(cal, i0-20)) {
			base.Kind, base.Reason = "skip", "ставка не снижается (доходность LQDT за 20 дней)"
			acts = append(acts, action{day: first.Day, order: 2, op: base, kind: "skip"})
			continue
		}
		close1 := first.Close
		if close1 <= 0 {
			continue // первый день без close — ждём
		}
		q := math.Floor(p.PerTrade * allocation(close1) / first.Face)
		if q < 1 {
			continue
		}
		acts = append(acts, action{day: first.Day, order: 2, kind: "buy", qty: q, px: first.Face, id: b.SecID, op: base})
		if i0+holdDays >= len(cal) {
			continue // окно ещё не закончилось
		}
		exit := cal[i0+holdDays].Day
		flows, err := p.Bonds.BondFlows(b.SecID)
		if err != nil {
			return nil, err
		}
		for _, f := range flows {
			if f.Day.After(first.Day) && !f.Day.After(exit) && f.ValueRub > 0 {
				kind := "coupon"
				if f.Amortizing {
					kind = "amortization"
				}
				acts = append(acts, action{day: f.Day, order: 1, kind: kind, px: f.ValueRub, id: b.SecID, op: base})
			}
		}
		var last *contract.BondBar
		for k := range bars {
			if bars[k].Day.After(exit) {
				break
			}
			if bars[k].Close > 0 {
				last = &bars[k]
			}
		}
		if last != nil {
			acts = append(acts, action{day: exit, order: 0, kind: "sell", px: last.Close*last.Face/100 + last.AccInt, id: b.SecID, op: base})
		}
	}
	sort.SliceStable(acts, func(i, j int) bool {
		if !acts[i].day.Equal(acts[j].day) {
			return acts[i].day.Before(acts[j].day)
		}
		if acts[i].order != acts[j].order {
			return acts[i].order < acts[j].order
		}
		return acts[i].op.SecID < acts[j].op.SecID
	})

	cash := p.Cap
	held := map[string]float64{}
	var ops []contract.Op
	for _, a := range acts {
		o := a.op
		o.Day = a.day.Format(time.DateOnly)
		switch a.kind {
		case "skip":
			ops = append(ops, o)
		case "buy":
			if a.qty*a.px > cash {
				o.Kind, o.Reason = "skip", "нет кэша"
				ops = append(ops, o)
				continue
			}
			cash -= a.qty * a.px
			held[a.id] = a.qty
			o.Kind, o.Qty, o.Price, o.Amount = "buy", a.qty, a.px, round2(-a.qty*a.px)
			ops = append(ops, o)
		case "coupon", "amortization":
			q := held[a.id]
			if q == 0 {
				continue
			}
			cash += q * a.px
			o.Kind, o.Qty, o.Price, o.Amount = a.kind, q, a.px, round2(q*a.px)
			ops = append(ops, o)
		case "sell":
			q := held[a.id]
			if q == 0 {
				continue
			}
			amt := q * a.px * (1 - cost)
			cash += amt
			delete(held, a.id)
			o.Kind, o.Qty, o.Price, o.Amount = "sell", q, a.px, round2(amt)
			o.Reason = "20-й торговый день"
			ops = append(ops, o)
		}
	}
	return ops, nil
}

func (p *Placements) bars(secid string, asOf time.Time) ([]contract.BondBar, error) {
	if b, ok := p.cache[secid]; ok {
		return b, nil
	}
	b, err := p.Bonds.BondBars(secid)
	if err != nil {
		return nil, err
	}
	b = b[:sort.Search(len(b), func(i int) bool { return !b[i].Day.Before(asOf) })]
	p.cache[secid] = b
	return b, nil
}

func (p *Placements) Mark(secid string, day time.Time) (float64, bool) {
	p.mmu.Lock()
	if p.marks == nil || time.Since(p.marksAt) > time.Hour {
		p.marks, p.marksAt = map[string][]contract.BondBar{}, time.Now()
	}
	b, ok := p.marks[secid]
	if !ok {
		var err error
		if b, err = p.Bonds.BondBars(secid); err != nil {
			p.mmu.Unlock()
			return 0, false
		}
		p.marks[secid] = b
	}
	p.mmu.Unlock()
	var px, face, acc float64
	for _, x := range b {
		if x.Day.After(day) {
			break
		}
		if x.Close > 0 {
			px = x.Close
		}
		face, acc = x.Face, x.AccInt
	}
	if px == 0 {
		return 0, false
	}
	return px*face/100 + acc, true
}
