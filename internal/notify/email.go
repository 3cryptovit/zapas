package notify

import (
	"context"
	"fmt"
	"mime"
	"net"
	"net/smtp"
	"strings"
	"time"
)

// EmailSender шлёт письма по SMTP. Локально письма ловит Mailpit (§5.5).
type EmailSender struct {
	addr string
	from string
	auth smtp.Auth
	// sendMail вынесен в поле, чтобы тест подменил отправку.
	sendMail func(addr string, a smtp.Auth, from string, to []string, msg []byte) error
}

func NewEmailSender(addr, from, user, password string) *EmailSender {
	s := &EmailSender{addr: addr, from: from, sendMail: smtp.SendMail}
	if user != "" {
		host, _, err := net.SplitHostPort(addr)
		if err != nil {
			host = addr
		}
		s.auth = smtp.PlainAuth("", user, password, host)
	}
	return s
}

// Configured сообщает, что канал можно использовать.
func (s *EmailSender) Configured() bool { return s != nil && s.addr != "" && s.from != "" }

// Send отправляет письмо.
func (s *EmailSender) Send(ctx context.Context, to string, payload Payload) error {
	if !s.Configured() {
		return fmt.Errorf("%w: не настроен SMTP", ErrPermanent)
	}
	if !strings.Contains(to, "@") {
		return fmt.Errorf("%w: некорректный адрес %q", ErrPermanent, to)
	}

	msg := buildMessage(s.from, to, payload)

	// smtp.SendMail не умеет context, поэтому ограничиваем время сами:
	// зависший SMTP не должен держать воркер.
	done := make(chan error, 1)
	go func() {
		done <- s.sendMail(s.addr, s.auth, s.from, []string{to}, msg)
	}()

	select {
	case err := <-done:
		if err != nil {
			return fmt.Errorf("email: отправка: %w", err)
		}
		return nil
	case <-ctx.Done():
		return ctx.Err()
	case <-time.After(20 * time.Second):
		return fmt.Errorf("email: SMTP не ответил за 20 секунд")
	}
}

// buildMessage собирает письмо. Тема кодируется base64 по RFC 2047:
// кириллица в заголовке иначе приедет мусором.
func buildMessage(from, to string, payload Payload) []byte {
	var b strings.Builder

	b.WriteString("From: " + from + "\r\n")
	b.WriteString("To: " + to + "\r\n")
	b.WriteString("Subject: " + encodeHeader(payload.Title) + "\r\n")
	b.WriteString("MIME-Version: 1.0\r\n")
	b.WriteString("Content-Type: text/plain; charset=UTF-8\r\n")
	b.WriteString("Content-Transfer-Encoding: 8bit\r\n")
	b.WriteString("\r\n")

	body := payload.Body
	if payload.Link != "" {
		body += "\n\n" + payload.Link
	}
	// В письме перевод строки — CRLF.
	b.WriteString(strings.ReplaceAll(body, "\n", "\r\n"))

	return []byte(b.String())
}

func encodeHeader(s string) string {
	if isASCII(s) {
		return s
	}
	// RFC 2047: кириллическая тема иначе приедет мусором.
	return mime.BEncoding.Encode("UTF-8", s)
}

func isASCII(s string) bool {
	for _, r := range s {
		if r > 127 {
			return false
		}
	}
	return true
}
