// Package ledger — сверка журнала операций с пересчётом: журнал «как будто реальные деньги»
// только дописывается, изменённое прошлое — ошибка, а не тихая перезапись. Общий для команд
// run/diary и сервиса paperd.
package ledger

import (
	"fmt"
	"math"
	"time"

	"tg-recs-audit/internal/contract"
)

// Reconcile сверяет журнал с пересчётом и возвращает, что дописать.
//
// В журнал попадают только операции дней раньше cutoff (сегодня по Москве): день,
// который ещё торгуется, может измениться. Записанные операции обязаны совпасть
// с пересчётом один в один — иначе прошлое счёта поменялось (правка кода, правил,
// таблицы корпоративных действий), и это решение человека, а не тихая перезапись.
func Reconcile(ledger, computed []contract.Op, cutoff time.Time) ([]contract.Op, error) {
	var settled []contract.Op
	for _, op := range computed {
		if op.Day < cutoff.Format(time.DateOnly) {
			settled = append(settled, op)
		}
	}
	if len(ledger) > len(settled) {
		return nil, fmt.Errorf("в журнале %d операций, а пересчёт даёт %d до %s: прошлое изменилось", len(ledger), len(settled), cutoff.Format(time.DateOnly))
	}
	for i, old := range ledger {
		if !same(old, settled[i]) {
			return nil, fmt.Errorf("операция #%d в журнале не совпадает с пересчётом:\n  журнал:   %s\n  пересчёт: %s\nпрошлое счёта изменилось — разберитесь и заведите новый журнал", old.Seq, show(old), show(settled[i]))
		}
	}
	return settled[len(ledger):], nil
}

func same(a, b contract.Op) bool {
	return a.Seq == b.Seq && a.Kind == b.Kind && a.Day == b.Day && a.SecID == b.SecID && a.PostID == b.PostID &&
		a.Reason == b.Reason && near(a.Qty, b.Qty) && near(a.Price, b.Price) && near(a.Amount, b.Amount)
}

func near(a, b float64) bool { return math.Abs(a-b) <= 1e-6*math.Max(1, math.Abs(a)) }

func show(o contract.Op) string {
	return fmt.Sprintf("%s %s %s пост %d qty %.4g по %.4g = %.2f %s", o.Day, o.Kind, o.SecID, o.PostID, o.Qty, o.Price, o.Amount, o.Reason)
}
