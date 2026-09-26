package httpx

import (
	"net/url"
	"strings"
)

// SameOrigin сообщает, пришёл ли запрос с того же сайта, что и публичный
// адрес приложения.
//
// Браузер присылает Origin без пути: "https://vitalness.ru". Публичный
// адрес путь содержит: "https://vitalness.ru/zapas". Сравнение строк
// целиком отвергло бы каждый запрос со своего же сайта.
func SameOrigin(origin, base string) bool {
	u, err := url.Parse(base)
	if err != nil || u.Scheme == "" || u.Host == "" {
		return false
	}
	return strings.EqualFold(origin, u.Scheme+"://"+u.Host)
}

// BasePath — путь публичного адреса без слеша на конце: "/zapas" или "".
func BasePath(base string) string {
	u, err := url.Parse(base)
	if err != nil {
		return ""
	}
	return strings.TrimRight(u.Path, "/")
}
