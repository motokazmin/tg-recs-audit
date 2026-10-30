package paper

import (
	"testing"
	"time"

	"tg-recs-audit/internal/contract"
)

// lateData — стратегия, у которой торги за вчера приходят из источника не сразу.
type lateData struct{ ops []contract.Op }

func (s *lateData) Name() string                           { return "late" }
func (s *lateData) Title() string                          { return "late" }
func (s *lateData) Benchmark() string                      { return "LQDT" }
func (s *lateData) Capital() float64                       { return 1_000_000 }
func (s *lateData) Start() time.Time                       { return day("2026-10-20") }
func (s *lateData) Mark(string, time.Time) (float64, bool) { return 0, false }
func (s *lateData) Ops(time.Time, Memory) ([]contract.Op, error) {
	return append([]contract.Op(nil), s.ops...), nil
}

type noAlert struct{}

func (noAlert) Alert(string) {}

// 2026-10-30, 02:00 МСК: ISS отдал за 29.10 строку ПСБ без сделок, но ещё не торги Воксиса —
// журнал записал пропуск ПСБ, а утренний пересчёт вставил покупку Воксиса перед ним и остановил
// placements. День D в журнал — только при asOf ≥ D+2, когда история за D уже полная.
func TestПоздниеТоргиЗаВчераНеМеняютЖурнал(t *testing.T) {
	psb := contract.Op{Kind: "skip", Day: "2026-10-29", SecID: "RU000A10GG34", Reason: "размещение не по номиналу"}
	voxis := contract.Op{Kind: "buy", Day: "2026-10-29", SecID: "RU000A10GFW8", Qty: 20, Price: 1000, Amount: -20000}
	s := &lateData{ops: []contract.Op{psb}}
	r := NewRunner(t.TempDir(), []Strategy{s}, noAlert{})

	r.RunAll(day("2026-10-30")) // ночью: торгов Воксиса ещё нет
	s.ops = []contract.Op{voxis, psb}
	r.RunAll(day("2026-10-30")) // утром по кнопке: пришли
	if st := r.Status("late"); st.Stopped {
		t.Fatalf("поздние данные за вчера остановили стратегию: %s", st.Err)
	}
	r.RunAll(day("2026-10-31"))
	got, err := r.Journal("late")
	if err != nil {
		t.Fatal(err)
	}
	if st := r.Status("late"); st.Stopped || len(got) != 2 || got[0].SecID != voxis.SecID {
		t.Fatalf("29.10 в журнале — обе операции на следующую ночь: %+v %s", got, st.Err)
	}
}
