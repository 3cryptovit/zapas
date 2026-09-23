package ratelimit

import (
	"crypto/rand"
	"encoding/hex"
)

// randSuffix различает обращения, попавшие в одну наносекунду: без него
// два одновременных запроса схлопнулись бы в один элемент множества.
func randSuffix() string {
	b := make([]byte, 4)
	if _, err := rand.Read(b); err != nil {
		// Источник энтропии недоступен — лимит важнее уникальности суффикса.
		return "x"
	}
	return hex.EncodeToString(b)
}
