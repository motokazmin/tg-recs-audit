package paperstrat

import (
	"testing"
	"time"

	"tg-recs-audit/internal/contract"
)

func TestАукционОФЗ_ПродажаПоОткрытиюСледующегоДня(t *testing.T) {
	o := &OFZAuctions{StartDay: dd("2026-10-19"), Cap: 1e6, PerTrade: 5e5,
		MM: func() ([]contract.Bar, error) {
			return []contract.Bar{{Day: dd("2026-10-21")}, {Day: dd("2026-10-22")}, {Day: dd("2026-10-28")}}, nil
		},
		Auctions: func(d time.Time) ([]contract.Auction, error) {
			if d.Equal(dd("2026-10-21")) || d.Equal(dd("2026-10-28")) { // тот же выпуск дважды
				return []contract.Auction{{SecID: "SU26248RMFS3", Day: d, AvgPrice: 80, Face: 1000, AccInt: 10}}, nil
			}
			return nil, nil
		},
		Bars: func(string, time.Time) ([]contract.BondBar, error) {
			return []contract.BondBar{
				{Day: dd("2026-10-21"), Open: 81, Close: 81, Face: 1000, AccInt: 10},
				{Day: dd("2026-10-22"), Open: 80.5, Close: 79, Face: 1000, AccInt: 10.3},
				{Day: dd("2026-10-28"), Open: 80, Close: 80, Face: 1000, AccInt: 12},
			}, nil
		},
		Flows: func(string) ([]contract.BondFlow, error) { return nil, nil },
	}
	ops, err := o.Ops(dd("2026-10-29"), mem{})
	if err != nil {
		t.Fatal(err)
	}
	if len(ops) != 3 || ops[0].Kind != "buy" || ops[1].Kind != "sell" || ops[2].Kind != "buy" {
		t.Fatalf("%+v", ops)
	}
	if ops[1].Price != 80.5 || ops[1].Day != "2026-10-22" {
		t.Fatalf("продажа не по открытию следующего дня: %+v", ops[1])
	}
	if ops[0].PostID == ops[2].PostID {
		t.Fatal("два аукциона одного выпуска слиплись в одну сделку")
	}
	// вход: 500 000 / (810 ₽ · 1.0005) → 616 шт.
	if ops[0].Qty != 616 {
		t.Fatalf("количество %v", ops[0].Qty)
	}
}
