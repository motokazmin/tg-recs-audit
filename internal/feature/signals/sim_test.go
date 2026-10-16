package signals

import (
	"math"
	"testing"
	"time"

	"tg-recs-audit/internal/contract"
)

func at(s string) time.Time { t, _ := time.Parse(time.DateTime, s); return t }

// Свечи по дням: bar(день, час, o, h, l, c).
type book map[string][]contract.Bar

func (b book) add(ts string, o, h, l, c float64) book {
	t := at(ts)
	d := t.Format(time.DateOnly)
	b[d] = append(b[d], contract.Bar{Day: t, Open: o, High: h, Low: l, Close: c})
	return b
}

func (b book) bars(_ string, day time.Time) ([]contract.Bar, error) {
	return b[day.Format(time.DateOnly)], nil
}

func long(price, stop float64) Signal {
	return Signal{PostID: 1, PostedAt: at("2025-01-06 10:05:00"), Asset: "BR", SecID: "BRH5", Long: true, Kind: Limit, Price: price, Stop: stop}
}

func run(t *testing.T, s Signal, b book) Outcome {
	t.Helper()
	o, err := Simulate(s, b.bars, time.Time{}, false, at("2025-02-01 00:00:00"))
	if err != nil {
		t.Fatal(err)
	}
	return o
}

func TestЛимитИТейк2R(t *testing.T) {
	b := book{}.
		add("2025-01-06 10:00:00", 101, 101, 99, 100).   // до поста — не считается
		add("2025-01-06 10:10:00", 101, 101, 99.5, 100). // фил по 100
		add("2025-01-06 10:20:00", 100, 103, 100, 102)   // 2R = 104 не достигнут
	b.add("2025-01-06 10:30:00", 102, 104.5, 102, 104) // тейк 104
	o := run(t, long(100, 98), b)
	want := 2 - costPerSide*(100+104)/2
	if o.Status != "filled" || o.Reason != "take" || math.Abs(o.R-want) > 1e-9 {
		t.Fatalf("%+v", o)
	}
}

func TestВСвечеФилаТолькоСтоп(t *testing.T) {
	b := book{}.add("2025-01-06 10:10:00", 101, 106, 97, 105) // и тейк, и стоп в свече фила
	o := run(t, long(100, 98), b)
	if o.Reason != "stop" || o.R > -1 {
		t.Fatalf("в свече фила — худший случай: %+v", o)
	}
}

func TestСтопНаГэпеПоOpen(t *testing.T) {
	b := book{}.add("2025-01-06 10:10:00", 101, 101, 99.9, 100).add("2025-01-06 10:20:00", 96, 97, 95, 96)
	o := run(t, long(100, 98), b)
	if o.Reason != "stop" || o.Exit != 96 {
		t.Fatalf("%+v", o)
	}
}

func TestВыходПоВремениЧерезПятьТорговыхДней(t *testing.T) {
	b := book{}.add("2025-01-06 10:10:00", 101, 101, 99.9, 100)
	for _, d := range []string{"2025-01-07", "2025-01-08", "2025-01-09", "2025-01-10", "2025-01-13", "2025-01-14"} {
		b.add(d+" 12:00:00", 100.5, 101, 100, 100.5)
	}
	o := run(t, long(100, 98), b)
	if o.Reason != "time" || !o.At.Equal(at("2025-01-13 12:00:00")) {
		t.Fatalf("5-й торговый день после фила — 13-е: %+v", o)
	}
}

func TestНоваяКарточкаСнимаетОрдер(t *testing.T) {
	b := book{}.add("2025-01-06 10:10:00", 101, 102, 100.5, 101).add("2025-01-06 11:10:00", 101, 101, 99, 100)
	o, _ := Simulate(long(100, 98), b.bars, at("2025-01-06 11:00:00"), false, at("2025-02-01 00:00:00"))
	if o.Status != "expired" {
		t.Fatalf("%+v", o)
	}
}
