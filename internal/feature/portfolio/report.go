package portfolio

import (
	"math"
	"sort"
	"time"
)

// Group — сводка по группе позиций.
type Group struct {
	N            int
	Wins         int     // доходность > 0
	MeanRet      float64 // средняя доходность позиции
	MedianRet    float64
	MeanExcess   float64 // средняя доходность минус бенчмарк за то же время
	MeanHoldDays float64
	Invested     float64 // руб.
	PnL          float64 // руб.
	BenchPnL     float64 // руб., если бы те же деньги в те же дни шли в бенчмарк
}

func Summarize(ps []Position) Group {
	g := Group{N: len(ps)}
	if g.N == 0 {
		return g
	}
	rets := make([]float64, 0, len(ps))
	for _, p := range ps {
		r := p.Return()
		rets = append(rets, r)
		if r > 0 {
			g.Wins++
		}
		g.MeanRet += r
		if b := p.Bench(); !math.IsNaN(b) {
			g.MeanExcess += r - b
			g.BenchPnL += p.Cost * b
		}
		g.MeanHoldDays += p.HoldDays()
		g.Invested += p.Cost
		g.PnL += p.Proceeds + p.DividendSum() - p.Cost
	}
	n := float64(g.N)
	g.MeanRet /= n
	g.MeanExcess /= n
	g.MeanHoldDays /= n
	sort.Float64s(rets)
	g.MedianRet = rets[len(rets)/2]
	return g
}

// PeakCapital — сколько денег было вложено одновременно на пике: столько понадобилось бы на самом деле.
func PeakCapital(ps []Position) (float64, time.Time) {
	type ev struct {
		day time.Time
		d   float64
	}
	var evs []ev
	for _, p := range ps {
		evs = append(evs, ev{p.EntryDay, p.Cost})
		if !p.Open() {
			evs = append(evs, ev{p.ExitDay, -p.Cost})
		}
	}
	// В один день сначала освобождаем, потом вкладываем: продажа утром даёт деньги на покупку.
	sort.Slice(evs, func(i, j int) bool {
		if !evs[i].day.Equal(evs[j].day) {
			return evs[i].day.Before(evs[j].day)
		}
		return evs[i].d < evs[j].d
	})
	var cur, peak float64
	var at time.Time
	for _, e := range evs {
		cur += e.d
		if cur > peak {
			peak, at = cur, e.day
		}
	}
	return peak, at
}

// XIRR — годовая доходность денежного потока (покупки −, продажи и оценка открытых +).
func XIRR(ps []Position, asOf time.Time, bench bool) float64 {
	type cf struct {
		t time.Time
		v float64
	}
	var flows []cf
	for _, p := range ps {
		out := p.Proceeds + p.DividendSum()
		if bench {
			b := p.Bench()
			if math.IsNaN(b) {
				continue
			}
			out = p.Cost * (1 + b)
		}
		flows = append(flows, cf{p.EntryDay, -p.Cost}, cf{p.ExitDay, out})
	}
	if len(flows) == 0 {
		return math.NaN()
	}
	t0 := flows[0].t
	for _, f := range flows {
		if f.t.Before(t0) {
			t0 = f.t
		}
	}
	npv := func(r float64) float64 {
		var s float64
		for _, f := range flows {
			s += f.v / math.Pow(1+r, f.t.Sub(t0).Hours()/24/365.25)
		}
		return s
	}
	lo, hi := -0.99, 10.0
	if npv(lo)*npv(hi) > 0 {
		return math.NaN()
	}
	for i := 0; i < 200; i++ {
		mid := (lo + hi) / 2
		if npv(lo)*npv(mid) <= 0 {
			hi = mid
		} else {
			lo = mid
		}
	}
	return (lo + hi) / 2
}
