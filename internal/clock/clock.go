// Package clock прячет источник времени за интерфейсом.
//
// Прямой вызов time.Now() в коде проекта запрещён (ADR-006): песочница живёт
// со смещёнными часами, и любой сервис, который спросит системное время
// напрямую, сломает симулятор.
package clock

import "time"

// Clock — источник текущего времени.
type Clock interface {
	Now() time.Time
}

// Func адаптирует функцию к интерфейсу Clock.
type Func func() time.Time

func (f Func) Now() time.Time { return f() }

// System — настоящие часы. Единственное место в проекте, где вызывается time.Now().
type System struct{}

func (System) Now() time.Time { return time.Now().UTC() }

// Offset — часы, смещённые на фиксированный интервал. Так работает виртуальное
// время песочницы: clock_offset хранится в тенанте и растёт при «промотке».
type Offset struct {
	Base   Clock
	Offset time.Duration
}

func NewOffset(base Clock, offset time.Duration) Offset {
	return Offset{Base: base, Offset: offset}
}

func (o Offset) Now() time.Time { return o.Base.Now().Add(o.Offset) }

// Fixed — застывшие часы для тестов.
type Fixed struct{ T time.Time }

func NewFixed(t time.Time) *Fixed { return &Fixed{T: t} }

func (f *Fixed) Now() time.Time { return f.T }

// Advance двигает застывшие часы вперёд.
func (f *Fixed) Advance(d time.Duration) { f.T = f.T.Add(d) }

// Set выставляет застывшие часы в конкретный момент.
func (f *Fixed) Set(t time.Time) { f.T = t }
