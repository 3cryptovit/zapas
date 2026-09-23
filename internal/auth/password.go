// Package auth отвечает за пароли, сессии, роли и CSRF (§12.2).
package auth

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"fmt"
	"strings"

	"golang.org/x/crypto/argon2"
)

// Параметры argon2id. Подобраны так, чтобы проверка занимала десятки
// миллисекунд на VPS 2 vCPU: достаточно дорого для перебора, незаметно
// для входа. Хранятся в самом хеше, поэтому их можно поднять позже, не ломая
// старые пароли.
const (
	argonTime    uint32 = 2
	argonMemory  uint32 = 64 * 1024 // 64 МБ
	argonThreads uint8  = 2
	argonKeyLen  uint32 = 32
	saltLen             = 16

	// MinPasswordLen — минимальная длина пароля. Длина важнее «обязательной
	// заглавной буквы»: правила состава гонят людей к «Password1!».
	MinPasswordLen = 10
	MaxPasswordLen = 256
)

// ErrPasswordTooShort и ErrPasswordTooLong — ошибки валидации пароля.
var (
	ErrPasswordTooShort = fmt.Errorf("пароль короче %d символов", MinPasswordLen)
	ErrPasswordTooLong  = fmt.Errorf("пароль длиннее %d символов", MaxPasswordLen)
	ErrInvalidHash      = errors.New("auth: неразборчивый формат хеша")
)

// HashPassword считает argon2id-хеш в стандартном PHC-формате:
// $argon2id$v=19$m=65536,t=2,p=2$<salt>$<hash>
func HashPassword(password string) (string, error) {
	if err := ValidatePassword(password); err != nil {
		return "", err
	}

	salt := make([]byte, saltLen)
	if _, err := rand.Read(salt); err != nil {
		return "", fmt.Errorf("auth: генерация соли: %w", err)
	}

	key := argon2.IDKey([]byte(password), salt, argonTime, argonMemory, argonThreads, argonKeyLen)

	return fmt.Sprintf("$argon2id$v=%d$m=%d,t=%d,p=%d$%s$%s",
		argon2.Version, argonMemory, argonTime, argonThreads,
		base64.RawStdEncoding.EncodeToString(salt),
		base64.RawStdEncoding.EncodeToString(key),
	), nil
}

// VerifyPassword сверяет пароль с хешем. Параметры берутся из самого хеша,
// поэтому пароли, посчитанные со старыми настройками, продолжают работать.
func VerifyPassword(password, encoded string) (bool, error) {
	params, salt, key, err := decodeHash(encoded)
	if err != nil {
		return false, err
	}

	candidate := argon2.IDKey([]byte(password), salt,
		params.time, params.memory, params.threads, uint32(len(key)))

	// Сравнение за постоянное время: обычное == протекает по времени.
	return subtle.ConstantTimeCompare(key, candidate) == 1, nil
}

// ValidatePassword проверяет длину. Считаем в рунах: «пароль из 10 символов»
// для пользователя — это 10 видимых символов, а не 10 байт.
func ValidatePassword(password string) error {
	switch n := len([]rune(password)); {
	case n < MinPasswordLen:
		return ErrPasswordTooShort
	case n > MaxPasswordLen:
		return ErrPasswordTooLong
	default:
		return nil
	}
}

type argonParams struct {
	memory  uint32
	time    uint32
	threads uint8
}

func decodeHash(encoded string) (argonParams, []byte, []byte, error) {
	parts := strings.Split(encoded, "$")
	if len(parts) != 6 || parts[1] != "argon2id" {
		return argonParams{}, nil, nil, ErrInvalidHash
	}

	var version int
	if _, err := fmt.Sscanf(parts[2], "v=%d", &version); err != nil {
		return argonParams{}, nil, nil, ErrInvalidHash
	}
	if version != argon2.Version {
		return argonParams{}, nil, nil, fmt.Errorf("auth: версия argon2 %d не поддерживается", version)
	}

	var p argonParams
	if _, err := fmt.Sscanf(parts[3], "m=%d,t=%d,p=%d", &p.memory, &p.time, &p.threads); err != nil {
		return argonParams{}, nil, nil, ErrInvalidHash
	}

	salt, err := base64.RawStdEncoding.Strict().DecodeString(parts[4])
	if err != nil {
		return argonParams{}, nil, nil, ErrInvalidHash
	}
	key, err := base64.RawStdEncoding.Strict().DecodeString(parts[5])
	if err != nil {
		return argonParams{}, nil, nil, ErrInvalidHash
	}
	return p, salt, key, nil
}

// dummyHash — хеш для несуществующего пользователя. Проверка пароля против
// него занимает столько же времени, сколько настоящая: иначе по времени
// ответа можно перебрать, какие email зарегистрированы (§12.2).
var dummyHash = func() string {
	h, err := HashPassword(strings.Repeat("x", MinPasswordLen))
	if err != nil {
		panic(err)
	}
	return h
}()

// BurnTime тратит столько же времени, сколько проверка настоящего пароля.
// Вызывается, когда пользователь с таким email не найден.
func BurnTime() {
	_, _ = VerifyPassword("wrong password", dummyHash)
}
