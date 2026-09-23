// Package ratelimit — скользящее окно на Redis.
//
// Используется в трёх местах с разными параметрами (§12.2, §7.5):
// подбор пароля (5 в минуту на IP и на email), создание песочницы
// (5 в час на IP), общий лимит API (300 в минуту на сессию).
package ratelimit

import (
	"context"
	"fmt"
	"time"

	"github.com/redis/go-redis/v9"
)

// Limiter считает обращения по ключу в скользящем окне.
type Limiter struct {
	rdb *redis.Client
}

func New(rdb *redis.Client) *Limiter { return &Limiter{rdb: rdb} }

// Result — что известно после попытки.
type Result struct {
	Allowed   bool
	Remaining int
	// RetryAfter — сколько ждать до следующей попытки, если лимит исчерпан.
	RetryAfter time.Duration
}

// Allow регистрирует обращение и говорит, укладывается ли оно в лимит.
//
// Окно скользящее по множеству отметок времени: точнее счётчика с фиксированным
// окном, где на стыке двух окон проходит двойной лимит.
func (l *Limiter) Allow(ctx context.Context, key string, limit int, window time.Duration) (Result, error) {
	if l == nil || l.rdb == nil {
		// Без Redis лимит не применяется: ограничитель не должен превращаться
		// в единственную точку отказа для входа в систему.
		return Result{Allowed: true, Remaining: limit}, nil
	}

	redisKey := "rl:" + key
	now := time.Now()
	cutoff := now.Add(-window)

	pipe := l.rdb.TxPipeline()
	// Выкидываем отметки старше окна.
	pipe.ZRemRangeByScore(ctx, redisKey, "0", fmt.Sprintf("%d", cutoff.UnixNano()))
	// Добавляем текущую. Член множества уникален, иначе одновременные
	// обращения в одну наносекунду схлопнулись бы в одно.
	pipe.ZAdd(ctx, redisKey, redis.Z{
		Score:  float64(now.UnixNano()),
		Member: fmt.Sprintf("%d-%s", now.UnixNano(), randSuffix()),
	})
	count := pipe.ZCard(ctx, redisKey)
	// Ключ живёт не дольше окна: мусор подчищается сам.
	pipe.Expire(ctx, redisKey, window)

	if _, err := pipe.Exec(ctx); err != nil {
		return Result{}, fmt.Errorf("ratelimit: %w", err)
	}

	used := int(count.Val())
	if used > limit {
		return Result{Allowed: false, RetryAfter: window}, nil
	}
	return Result{Allowed: true, Remaining: limit - used}, nil
}

// Reset снимает лимит по ключу. Вызывается после успешного входа: удачная
// попытка не должна оставлять следов для следующей.
func (l *Limiter) Reset(ctx context.Context, key string) error {
	if l == nil || l.rdb == nil {
		return nil
	}
	return l.rdb.Del(ctx, "rl:"+key).Err()
}

// Count возвращает число обращений в окне, не регистрируя новое.
func (l *Limiter) Count(ctx context.Context, key string, window time.Duration) (int, error) {
	if l == nil || l.rdb == nil {
		return 0, nil
	}
	cutoff := time.Now().Add(-window)
	n, err := l.rdb.ZCount(ctx, "rl:"+key, fmt.Sprintf("%d", cutoff.UnixNano()), "+inf").Result()
	if err != nil {
		return 0, fmt.Errorf("ratelimit: %w", err)
	}
	return int(n), nil
}
