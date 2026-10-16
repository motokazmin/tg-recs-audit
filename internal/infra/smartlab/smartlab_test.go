package smartlab

import (
	"os"
	"testing"
)

// Снимок страницы SBER: SBER и SBERP вперемешку, суммы с запятой и ₽.
func TestParseСнимкаSBER(t *testing.T) {
	b, err := os.ReadFile("testdata/sber.html")
	if err != nil {
		t.Skip("нет снимка")
	}
	rows, err := Parse(string(b))
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) < 10 {
		t.Fatalf("строк %d", len(rows))
	}
	r := rows[0]
	if r.SecID != "SBER" || r.LastBuyDay != "2026-07-17" || r.RecordDay != "2026-07-20" || r.Amount != 37.64 {
		t.Fatalf("%+v", r)
	}
}
