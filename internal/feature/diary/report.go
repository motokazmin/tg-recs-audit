package diary

import (
	"math"
	"time"
)

// Summary — итог счёта на последний день.
type Summary struct {
	Day         time.Time
	Contributed float64
	Equity      float64
	Bench       float64
	MoneyMarket float64
	XIRR        float64 // годовая доходность взносов
	BenchXIRR   float64
	MMXIRR      float64 // те же взносы в фонд денежного рынка (LQDT)
	MaxDrawdown float64 // худшее (капитал − взносы) / взносы
	ByKind      map[string]float64
}

func Summarize(r Result) Summary {
	s := Summary{ByKind: map[string]float64{}}
	if len(r.Curve) == 0 {
		return s
	}
	last := r.Curve[len(r.Curve)-1]
	s.Day, s.Contributed, s.Equity, s.Bench, s.MoneyMarket = last.Day, last.Contributed, last.Equity(), last.Bench, last.MoneyMarket
	type cf struct {
		t time.Time
		v float64
	}
	var flows []cf
	for _, o := range r.Ops {
		s.ByKind[o.Kind] += o.Amount
		if o.Kind == "contribution" {
			flows = append(flows, cf{o.Day, -o.Amount})
		}
	}
	for _, p := range r.Curve {
		if p.Contributed > 0 {
			if dd := (p.Equity() - p.Contributed) / p.Contributed; dd < s.MaxDrawdown {
				s.MaxDrawdown = dd
			}
		}
	}
	irr := func(final float64) float64 {
		fs := append(append([]cf(nil), flows...), cf{s.Day, final})
		npv := func(r float64) float64 {
			var v float64
			for _, f := range fs {
				v += f.v / math.Pow(1+r, f.t.Sub(fs[0].t).Hours()/24/365.25)
			}
			return v
		}
		lo, hi := -0.99, 5.0
		if npv(lo)*npv(hi) > 0 {
			return math.NaN()
		}
		for i := 0; i < 200; i++ {
			m := (lo + hi) / 2
			if npv(lo)*npv(m) <= 0 {
				hi = m
			} else {
				lo = m
			}
		}
		return (lo + hi) / 2
	}
	s.XIRR, s.BenchXIRR, s.MMXIRR = irr(s.Equity), irr(s.Bench), irr(s.MoneyMarket)
	return s
}
