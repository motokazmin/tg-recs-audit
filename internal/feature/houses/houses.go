// Package houses — идеи по аналитическим домам: у кого из них есть умение, а у кого везение.
//
// Каждый дом — отдельный портфель по тем же правилам (иначе «уже в портфеле» смешивает дома).
// Выбрать лучший по тем же данным, на которых его оцениваем, — подгонка: домов ~45.
// Поэтому период делится на две части, и смотрится, сохраняется ли порядок домов во второй:
// ранговая корреляция близко к нулю — рейтинг первого периода ничего не предсказывает.
package houses

import (
	"math"
	"sort"
	"time"

	"tg-recs-audit/internal/feature/ideas"
	"tg-recs-audit/internal/feature/portfolio"
)

// Stat — превышение доходности над бенчмарком по позициям группы.
type Stat struct {
	N          int
	MeanExcess float64
	T          float64 // t-статистика среднего превышения; |t| < 2 — от нуля не отличить
	MeanRet    float64
	WinRate    float64
}

type Row struct {
	House        string
	All, In, Out Stat
}

type Report struct {
	Rows     []Row
	Split    time.Time
	RankCorr float64 // Спирмен по MeanExcess In vs Out у домов с ≥ minN в обоих периодах
	RankedN  int
}

// RunFunc — прогон портфеля по подмножеству идей (собирает composition root).
type RunFunc func([]ideas.Idea) ([]portfolio.Position, error)

func Compare(all []ideas.Idea, house func(string) string, run RunFunc, split time.Time, minN int) (Report, error) {
	by := map[string][]ideas.Idea{}
	for _, i := range all {
		by[house(i.Analyst)] = append(by[house(i.Analyst)], i)
	}
	rep := Report{Split: split}
	for h, ii := range by {
		if len(ii) < minN {
			continue
		}
		ps, err := run(ii)
		if err != nil {
			return rep, err
		}
		var in, out []portfolio.Position
		for _, p := range ps {
			if p.EntryDay.Before(split) {
				in = append(in, p)
			} else {
				out = append(out, p)
			}
		}
		rep.Rows = append(rep.Rows, Row{House: h, All: stat(ps), In: stat(in), Out: stat(out)})
	}
	sort.Slice(rep.Rows, func(i, j int) bool { return rep.Rows[i].All.MeanExcess > rep.Rows[j].All.MeanExcess })

	var a, b []float64
	for _, r := range rep.Rows {
		if r.In.N >= minN && r.Out.N >= minN {
			a = append(a, r.In.MeanExcess)
			b = append(b, r.Out.MeanExcess)
		}
	}
	rep.RankedN = len(a)
	rep.RankCorr = spearman(a, b)
	return rep, nil
}

func stat(ps []portfolio.Position) Stat {
	s := Stat{}
	var ex []float64
	for _, p := range ps {
		b := p.Bench()
		if math.IsNaN(b) {
			continue
		}
		r := p.Return()
		ex = append(ex, r-b)
		s.MeanRet += r
		if r > 0 {
			s.WinRate++
		}
	}
	s.N = len(ex)
	if s.N == 0 {
		return s
	}
	n := float64(s.N)
	s.MeanRet /= n
	s.WinRate /= n
	for _, e := range ex {
		s.MeanExcess += e
	}
	s.MeanExcess /= n
	if s.N > 1 {
		var v float64
		for _, e := range ex {
			v += (e - s.MeanExcess) * (e - s.MeanExcess)
		}
		sd := math.Sqrt(v / (n - 1))
		if sd > 0 {
			s.T = s.MeanExcess / (sd / math.Sqrt(n))
		}
	}
	return s
}

func spearman(a, b []float64) float64 {
	if len(a) < 3 {
		return math.NaN()
	}
	ra, rb := ranks(a), ranks(b)
	var ma, mb float64
	for i := range ra {
		ma += ra[i]
		mb += rb[i]
	}
	n := float64(len(ra))
	ma, mb = ma/n, mb/n
	var cov, va, vb float64
	for i := range ra {
		cov += (ra[i] - ma) * (rb[i] - mb)
		va += (ra[i] - ma) * (ra[i] - ma)
		vb += (rb[i] - mb) * (rb[i] - mb)
	}
	return cov / math.Sqrt(va*vb)
}

func ranks(x []float64) []float64 {
	idx := make([]int, len(x))
	for i := range idx {
		idx[i] = i
	}
	sort.Slice(idx, func(i, j int) bool { return x[idx[i]] < x[idx[j]] })
	r := make([]float64, len(x))
	for k := 0; k < len(idx); {
		m := k
		for m+1 < len(idx) && x[idx[m+1]] == x[idx[k]] {
			m++
		}
		for t := k; t <= m; t++ {
			r[idx[t]] = float64(k+m)/2 + 1 // средний ранг для одинаковых
		}
		k = m + 1
	}
	return r
}
