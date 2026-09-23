package auth

import (
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"fmt"
)

// TokenBytes — длина случайного токена сессии (§12.2). 32 байта — это 256 бит
// энтропии: перебор невозможен, а cookie остаётся короткой.
const TokenBytes = 32

// NewToken выдаёт новый токен сессии и его хеш. Наружу уходит токен,
// в БД ложится только хеш: дамп базы не даёт войти ни под кем.
func NewToken() (token string, hash []byte, err error) {
	raw := make([]byte, TokenBytes)
	if _, err := rand.Read(raw); err != nil {
		return "", nil, fmt.Errorf("auth: генерация токена: %w", err)
	}
	token = base64.RawURLEncoding.EncodeToString(raw)
	return token, HashToken(token), nil
}

// HashToken считает SHA-256 токена.
//
// Здесь достаточно быстрого хеша, в отличие от паролей: токен случаен и имеет
// 256 бит энтропии, перебирать его по словарю нечем.
func HashToken(token string) []byte {
	sum := sha256.Sum256([]byte(token))
	return sum[:]
}

// EqualTokens сравнивает хеши за постоянное время.
func EqualTokens(a, b []byte) bool { return subtle.ConstantTimeCompare(a, b) == 1 }

// NewCSRFToken выдаёт токен для double-submit: один экземпляр уходит
// в cookie, второй фронтенд шлёт в заголовке X-CSRF-Token (§12.2).
func NewCSRFToken() (token string, hash []byte, err error) {
	return NewToken()
}
