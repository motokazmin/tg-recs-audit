package ideas

import (
	"testing"
	"time"

	"tg-recs-audit/internal/contract"
)

var known = map[string]bool{"SBER": true, "TATNP": true, "MBNK": true}

func msg(id int64, text string) contract.Message {
	return contract.Message{ID: id, PostedAt: time.Date(2024, 1, 17, 7, 0, 0, 0, time.UTC), Text: text}
}

func TestСтарыйШаблон(t *testing.T) {
	got, rej := Parse([]contract.Message{msg(1, "Инвестидея по акции Сбербанк #SBER\n\n• Целевая цена: 3 540 ₽ (+12,1%)\n• Дата идеи: 9 января 2024\n• Аналитики: Finam Alert\n\nУзнавайте…")}, known)
	if len(rej) != 0 || len(got) != 1 {
		t.Fatalf("got %v rej %v", got, rej)
	}
	i := got[0]
	if i.SecID != "SBER" || i.Target != 3540 || i.Analyst != "Finam Alert" || i.IdeaDate != time.Date(2024, 1, 9, 0, 0, 0, 0, time.UTC) {
		t.Fatalf("%+v", i)
	}
}

func TestНовыйШаблон(t *testing.T) {
	text := "Инвестидея по акции МТС-Банк\n\nТ-Инвестиции, идея от 9 октября 2026:\n• Цена идеи: 935,5 ₽\n• Целевая цена: 1 250 ₽\n• Потенциал: +33,62% к цене идеи\n\nПрогнозы аналитиков →\n\n#MBNK\n\n@investmint • @divalerts"
	got, rej := Parse([]contract.Message{msg(2, text)}, known)
	if len(rej) != 0 || len(got) != 1 {
		t.Fatalf("got %v rej %v", got, rej)
	}
	i := got[0]
	if i.SecID != "MBNK" || i.Target != 1250 || i.Analyst != "Т-Инвестиции" || i.IdeaDate != time.Date(2026, 10, 9, 0, 0, 0, 0, time.UTC) {
		t.Fatalf("%+v", i)
	}
}

func TestПрогнозСРейтингом(t *testing.T) {
	got, _ := Parse([]contract.Message{msg(3, "Прогноз по акции Татнефть АП #TATNP\n\n• Прогнозная цена: 639,5 ₽ (+19,4%)\n• Прогноз: Держать\n• Дата: 8 апреля 2021\n• Аналитики: Газпромбанк")}, known)
	if len(got) != 1 || got[0].Kind != "прогноз" || got[0].Rating != "Держать" || got[0].Target != 639.5 {
		t.Fatalf("%+v", got)
	}
}

func TestОтказыСПричиной(t *testing.T) {
	msgs := []contract.Message{
		msg(4, "Инвестидея по акции Неизвестная #NOPE\n• Целевая цена: 10 ₽"),
		msg(5, "Инвестидея по акции Сбербанк #SBER\n• Дата идеи: 9 января 2024"),
		msg(6, "Ближайшие отсечки\n#SBER"), // не идея — не отказ
	}
	got, rej := Parse(msgs, known)
	if len(got) != 0 || len(rej) != 2 || rej[0].PostID != 4 || rej[1].PostID != 5 {
		t.Fatalf("got %v rej %v", got, rej)
	}
}
