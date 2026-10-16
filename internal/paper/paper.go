// Package paper — каркас проверки идей вперёд на виртуальных деньгах (ADR 0005).
//
// Стратегия — функция «все операции с даты старта по данным, известным к asOf».
// Каркас раз в сутки пересчитывает каждую стратегию, дописывает новые операции в её журнал
// через ledger.Reconcile (изменённое прошлое — остановка и тревога) и оценивает счёт.
// О конкретных стратегиях каркас не знает.
package paper

import (
	"time"

	"tg-recs-audit/internal/contract"
)

// Strategy — то, что каркас требует от проверяемой идеи.
type Strategy interface {
	Name() string      // слаг: имя журнала и URL
	Title() string     // для админки
	Benchmark() string // секция бенчмарка в админке: «MCFTR», «LQDT»
	Capital() float64  // стартовый счёт, руб.
	Start() time.Time  // день старта (по Москве)
	// Ops — все операции с Start по данным, известным к asOf; в журнал попадут дни раньше asOf.
	// mem — память стратегии между запусками: что и когда она увидела (пост, подтверждение
	// выплаты), чтобы пересчёт был детерминирован, даже если источник потом изменится.
	Ops(asOf time.Time, mem Memory) ([]contract.Op, error)
	// Mark — цена бумаги на конец дня для оценки открытой позиции, руб. за штуку (с НКД).
	Mark(secid string, day time.Time) (float64, bool)
}

// Memory — ключ-значение, сохраняется каркасом между запусками.
type Memory interface {
	Get(key string) (string, bool)
	Set(key, value string)
	Keys(prefix string) []string
}

// Alerter — тревога в Telegram (или в лог).
type Alerter interface {
	Alert(msg string)
}

// Bench — дневные бары бенчмарка (MCFTR, LQDT) для сравнения.
type Bench interface {
	Bars(name string) ([]contract.Bar, error)
}

// Status — итог последнего пересчёта стратегии.
type Status struct {
	At      time.Time
	Added   int
	Total   int
	Err     string
	Stopped bool // журнал разошёлся с пересчётом — стратегия остановлена до решения человека
}
