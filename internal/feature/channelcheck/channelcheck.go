// Package channelcheck — пригодна ли история канала для проверки рекомендаций.
//
// Отвечает на вопросы до любого бэктеста: сколько лет истории, сколько постов
// удалено (дыры в нумерации ID), сколько отредактировано, о каких бумагах канал
// пишет и какая доля постов вообще похожа на рекомендацию.
package channelcheck

import (
	"regexp"
	"sort"
	"time"

	"tg-recs-audit/internal/contract"
	"tg-recs-audit/internal/feature/mentions"
)

// Слова действия — грубая оценка «пост похож на рекомендацию», не разбор сигнала.
var actionWords = regexp.MustCompile(`(?i)(покупа|купит|купил|продава|продат|продал|` +
	`шорт|лонг|\blong\b|\bshort\b|целев|таргет|target|стоп|тейк|фиксир|набира|докупа|` +
	`позици|вход|рекоменд|upside|апсайд|потенциал рост)`)

type Hole struct {
	AfterID, BeforeID int64
	Missing           int64
	From, To          time.Time
}

type Report struct {
	Info        contract.ChannelInfo
	Posts       int
	First, Last time.Time
	PerYear     map[int]int

	IDRange  int64 // max−min+1
	Missing  int64 // ID без поста: удалённые + служебные сообщения
	TopHoles []Hole

	Edited int

	WithTicker int // постов с хотя бы одним тикером
	WithAction int // с тикером и словом действия — кандидат в рекомендацию
	Tickers    []mentions.Mention
}

func Check(info contract.ChannelInfo, msgs []contract.Message, idx mentions.Index) Report {
	r := Report{Info: info, Posts: len(msgs), PerYear: map[int]int{}}
	if len(msgs) == 0 {
		return r
	}
	r.First, r.Last = msgs[0].PostedAt, msgs[0].PostedAt
	for _, m := range msgs {
		if m.PostedAt.Before(r.First) {
			r.First = m.PostedAt
		}
		if m.PostedAt.After(r.Last) {
			r.Last = m.PostedAt
		}
		r.PerYear[m.PostedAt.Year()]++
		if m.Edited {
			r.Edited++
		}
		if t := idx.InPost(m); len(t) > 0 {
			r.WithTicker++
			if actionWords.MatchString(m.Text) {
				r.WithAction++
			}
		}
	}
	r.Tickers = mentions.Find(msgs, idx)

	// Дыры в нумерации. Пост занимает IDSpan номеров (альбом из N фото — N).
	sorted := append([]contract.Message(nil), msgs...)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i].ID < sorted[j].ID })
	r.IDRange = sorted[len(sorted)-1].ID - sorted[0].ID + 1
	var holes []Hole
	for i := 1; i < len(sorted); i++ {
		a, b := sorted[i-1], sorted[i]
		next := a.ID + int64(max(a.IDSpan, 1))
		if gap := b.ID - next; gap > 0 {
			r.Missing += gap
			holes = append(holes, Hole{a.ID, b.ID, gap, a.PostedAt, b.PostedAt})
		}
	}
	sort.Slice(holes, func(i, j int) bool { return holes[i].Missing > holes[j].Missing })
	r.TopHoles = holes[:min(len(holes), 8)]
	return r
}
