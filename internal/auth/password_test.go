package auth_test

import (
	"strings"
	"testing"

	"github.com/vostapenko/zapas/internal/auth"
)

func TestHashPassword_ФорматPHC(t *testing.T) {
	hash, err := auth.HashPassword("правильный-пароль")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(hash, "$argon2id$v=19$m=65536,t=2,p=2$") {
		t.Errorf("хеш не в формате PHC: %s", hash)
	}
	// Пароль в хеше искать бессмысленно, но проверка дешёвая.
	if strings.Contains(hash, "правильный-пароль") {
		t.Error("пароль попал в хеш в открытом виде")
	}
}

func TestHashPassword_СольРазнаяКаждыйРаз(t *testing.T) {
	first, err := auth.HashPassword("одинаковый-пароль")
	if err != nil {
		t.Fatal(err)
	}
	second, err := auth.HashPassword("одинаковый-пароль")
	if err != nil {
		t.Fatal(err)
	}
	if first == second {
		t.Error("два хеша одного пароля совпали: соль не случайная")
	}
}

func TestVerifyPassword(t *testing.T) {
	const password = "молоко-сливки-42"
	hash, err := auth.HashPassword(password)
	if err != nil {
		t.Fatal(err)
	}

	ok, err := auth.VerifyPassword(password, hash)
	if err != nil {
		t.Fatal(err)
	}
	if !ok {
		t.Error("верный пароль не принят")
	}

	ok, err = auth.VerifyPassword(password+"x", hash)
	if err != nil {
		t.Fatal(err)
	}
	if ok {
		t.Error("неверный пароль принят")
	}
}

func TestVerifyPassword_ИспорченныйХеш(t *testing.T) {
	tests := []string{
		"",
		"не хеш вовсе",
		"$argon2i$v=19$m=65536,t=2,p=2$c2FsdA$aGFzaA",  // другой вариант argon2
		"$argon2id$v=13$m=65536,t=2,p=2$c2FsdA$aGFzaA", // неподдерживаемая версия
		"$argon2id$v=19$m=65536$c2FsdA$aGFzaA",         // не хватает параметров
		"$argon2id$v=19$m=65536,t=2,p=2$не-base64$aGFzaA",
	}
	for _, encoded := range tests {
		if _, err := auth.VerifyPassword("пароль", encoded); err == nil {
			t.Errorf("VerifyPassword(%q) не вернул ошибку", encoded)
		}
	}
}

func TestValidatePassword(t *testing.T) {
	tests := []struct {
		name     string
		password string
		wantErr  bool
	}{
		{"ровно минимум", strings.Repeat("a", 10), false},
		{"на символ короче", strings.Repeat("a", 9), true},
		{"пустой", "", true},
		{"слишком длинный", strings.Repeat("a", 257), true},
		// Длина считается в рунах: «пароль» из 10 кириллических символов
		// занимает 20 байт, но для пользователя это 10 символов.
		{"кириллица считается по символам", "паролище12", false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			err := auth.ValidatePassword(tc.password)
			if (err != nil) != tc.wantErr {
				t.Errorf("ValidatePassword: err = %v, wantErr = %v", err, tc.wantErr)
			}
		})
	}
}

func TestHashPassword_КороткийПарольНеХешируется(t *testing.T) {
	if _, err := auth.HashPassword("короткий"); err == nil {
		t.Error("короткий пароль не должен хешироваться")
	}
}

func TestBurnTime_НеПадает(t *testing.T) {
	// Вызывается для несуществующего email, чтобы ответ занимал столько же
	// времени, сколько настоящая проверка.
	auth.BurnTime()
}

func TestNewToken(t *testing.T) {
	token, hash, err := auth.NewToken()
	if err != nil {
		t.Fatal(err)
	}
	if len(token) < 40 {
		t.Errorf("токен подозрительно короткий: %d символов", len(token))
	}
	if len(hash) != 32 {
		t.Errorf("длина SHA-256 = %d, want 32", len(hash))
	}
	if !auth.EqualTokens(hash, auth.HashToken(token)) {
		t.Error("хеш не совпал с пересчитанным")
	}

	other, _, err := auth.NewToken()
	if err != nil {
		t.Fatal(err)
	}
	if token == other {
		t.Error("два токена подряд совпали")
	}
	if auth.EqualTokens(hash, auth.HashToken(other)) {
		t.Error("хеши разных токенов совпали")
	}
}
