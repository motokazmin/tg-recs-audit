package diary

import (
	"testing"
	"time"

	"tg-recs-audit/internal/contract"
)

var since = time.Date(2023, 1, 1, 0, 0, 0, 0, time.UTC)

func post(id int64, day string, text string, tags ...string) contract.Message {
	t, _ := time.Parse(time.DateOnly, day)
	return contract.Message{ID: id, PostedAt: t.Add(9 * time.Hour), Text: text, Tags: tags}
}

func parse(t *testing.T, ms []contract.Message, aliases map[string]string) ([]Contribution, []Trade, []Holding, []Unparsed) {
	t.Helper()
	known := map[string]bool{"SBER": true, "MGNT": true, "UNAC": true, "PIKK": true}
	cs, ts, hs, _, un := Parse(ms, since, known, aliases, nil)
	return cs, ts, hs, un
}

func TestИсполнениеРазмещенияЗаменяетЗаявку(t *testing.T) {
	_, ts, _, un := parse(t, []contract.Message{
		post(1, "2025-02-27", "⚫️Система, заявка на покупку: 200 лотов (200шт) 200000р\n$RU000A10B024", "Покупка"),
		post(2, "2025-03-05", "— Я участвовал в первичном размещении.\nПодавал заявку на 200000р, брокер Тинькоф исполнил заявку на 100%.\n$RU000A10B024", "Облигации"),
	}, nil)
	if len(un) != 0 || len(ts) != 1 || !ts[0].Placement || ts[0].PostID != 2 || ts[0].Amount != 200000 {
		t.Fatalf("сделка — исполнение, заявка не считается: %+v %+v", ts, un)
	}
}

func TestИсполнениеСоШтукамиИПроцентом(t *testing.T) {
	_, ts, _, _ := parse(t, []contract.Message{
		post(1, "2025-03-17", "Подавал заявку на 125000р, брокер Тинькоф исполнил заявку на ~85% (106шт).\n$RU000A10B2P6", "Облигации"),
	}, nil)
	if len(ts) != 1 || ts[0].Qty != 106 || ts[0].Amount != 106250 {
		t.Fatalf("%+v", ts)
	}
}

func TestНесколькоСделокБезБумагиНеПодставляютТегПоста(t *testing.T) {
	_, ts, _, un := parse(t, []contract.Message{
		post(1, "2024-12-23", "— Продал актив Магнит (14 лотов) 14шт - 69883р\n— Продал актив Неизвестно (28 лотов) 28шт - 73444р", "Продажа", "SBER"),
	}, map[string]string{"Магнит": "MGNT"})
	if len(ts) != 0 || len(un) != 1 {
		t.Fatalf("строка без бумаги в посте с несколькими сделками — в неразобранные: %+v", ts)
	}
}

func TestЯвныйISINВажнееУпомянутойАкции(t *testing.T) {
	_, ts, _, _ := parse(t, []contract.Message{
		post(1, "2024-01-15", "— Ну и сразу реализуем пополнение и дивиденды от #Магнит, покупаем: АПРИ Флай: 11 лота (11шт) 11717.64р\n$RU000A107FZ5", "Покупка"),
	}, map[string]string{"Магнит": "MGNT"})
	if len(ts) != 1 || ts[0].Ref != "RU000A107FZ5" {
		t.Fatalf("%+v", ts)
	}
}

func TestОбщаяПозицияЭтоСверкаАНеСделка(t *testing.T) {
	_, ts, hs, _ := parse(t, []contract.Message{
		post(1, "2023-09-18", "Сбербанк покупка:\n2 лота (20шт) 5146.99р\n⚡️Общая позиция 40 шт, средняя цена 259,39", "Покупка", "SBER"),
	}, nil)
	if len(ts) != 1 || ts[0].Qty != 20 || len(hs) != 1 || hs[0].Qty != 40 || hs[0].Ref != "SBER" {
		t.Fatalf("сделки %+v позиции %+v", ts, hs)
	}
}

func TestСуммаСРублёмПослеКоличества(t *testing.T) {
	_, ts, _, _ := parse(t, []contract.Message{
		post(1, "2024-02-05", "— Продал актив Самолёт 3шт (3 лота) - 11337р", "Продажа", "SBER"),
		post(2, "2025-11-07", "Купил 240шт (240 лотов). Сумма покупки 241584,2р (Вся сумма заемные)\n$RU000A10C8F3", "Покупка"),
	}, nil)
	if len(ts) != 2 || ts[0].Amount != 11337 || ts[1].Amount != 241584.2 {
		t.Fatalf("«р» на кириллице — \\b в Go не работает: %+v", ts)
	}
}

func TestВзносОдинНомерВТечениеТрёхДней(t *testing.T) {
	cs, _, _, _ := parse(t, []contract.Message{
		post(1, "2023-06-19", "#Взнос19"),
		post(2, "2023-06-19", "#Взнос19\nНовая неделя. Ещё 10000р"),
		post(3, "2025-05-26", "#Взнос119 Ещё 10000р"),
		post(4, "2025-06-02", "#Взнос119 Ещё 10000р"), // автор забыл прибавить номер — взнос новый
	}, nil)
	if len(cs) != 3 {
		t.Fatalf("%+v", cs)
	}
}
