package portfolio

import (
	"strings"
	"testing"

	"tg-recs-audit/internal/contract"
)

func op(seq int, day, kind string, price float64) contract.Op {
	return contract.Op{Seq: seq, Day: day, Kind: kind, SecID: "SBER", Price: price, PostID: int64(seq)}
}

func TestReconcileДописываетТолькоЗакрытыеДни(t *testing.T) {
	computed := []contract.Op{op(1, "2024-01-10", "buy", 100), op(2, "2024-01-11", "sell", 110), op(3, "2024-01-12", "buy", 105)}
	fresh, err := Reconcile(computed[:1], computed, d("2024-01-12"))
	if err != nil || len(fresh) != 1 || fresh[0].Seq != 2 {
		t.Fatalf("день 2024-01-12 ещё торгуется — в журнал только #2: %v %v", fresh, err)
	}
}

func TestReconcileПадаетЕслиПрошлоеИзменилось(t *testing.T) {
	ledger := []contract.Op{op(1, "2024-01-10", "buy", 100)}
	_, err := Reconcile(ledger, []contract.Op{op(1, "2024-01-10", "buy", 101)}, d("2024-02-01"))
	if err == nil || !strings.Contains(err.Error(), "не совпадает") {
		t.Fatalf("другая цена в прошлом обязана остановить прогон: %v", err)
	}
	_, err = Reconcile(ledger, nil, d("2024-02-01"))
	if err == nil {
		t.Fatal("пропавшая операция обязана остановить прогон")
	}
}
