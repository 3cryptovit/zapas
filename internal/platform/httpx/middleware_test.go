package httpx_test

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/vostapenko/zapas/internal/platform/httpx"
)

// TestRealIP_ЗаголовокНеПодделать — §7.5 и §12.2.
//
// От адреса клиента зависят оба ограничителя: пять демо в час и защита
// от перебора пароля. Если заголовку верить безоговорочно, их снимает
// любой, кто умеет слать HTTP.
func TestRealIP_ЗаголовокНеПодделать(t *testing.T) {
	tests := []struct {
		name       string
		remoteAddr string
		headers    map[string]string
		want       string
	}{
		{
			name:       "от прокси на петле заголовку верим",
			remoteAddr: "127.0.0.1:54321",
			headers:    map[string]string{"X-Real-IP": "203.0.113.7"},
			want:       "203.0.113.7:0",
		},
		{
			name:       "снаружи заголовок игнорируется",
			remoteAddr: "198.51.100.9:44444",
			headers:    map[string]string{"X-Real-IP": "203.0.113.7"},
			want:       "198.51.100.9:44444",
		},
		{
			name:       "X-Forwarded-For не трогаем вовсе",
			remoteAddr: "127.0.0.1:54321",
			headers:    map[string]string{"X-Forwarded-For": "203.0.113.7"},
			want:       "127.0.0.1:54321",
		},
		{
			name:       "мусор в заголовке не ломает разбор",
			remoteAddr: "127.0.0.1:54321",
			headers:    map[string]string{"X-Real-IP": "не-адрес"},
			want:       "127.0.0.1:54321",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			var got string
			h := httpx.RealIP(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
				got = r.RemoteAddr
			}))

			req := httptest.NewRequest(http.MethodGet, "/", nil)
			req.RemoteAddr = tc.remoteAddr
			for k, v := range tc.headers {
				req.Header.Set(k, v)
			}
			h.ServeHTTP(httptest.NewRecorder(), req)

			if got != tc.want {
				t.Errorf("RemoteAddr = %q, ждали %q", got, tc.want)
			}
		})
	}
}
