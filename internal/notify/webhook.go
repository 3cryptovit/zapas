package notify

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"strconv"
	"strings"

	"github.com/go-chi/chi/v5"

	"github.com/vostapenko/zapas/internal/auth"
	"github.com/vostapenko/zapas/internal/platform/httpx"
)

// TelegramHandler обслуживает привязку чата и входящие обновления бота.
type TelegramHandler struct {
	svc    *Service
	sender *TelegramSender
	// secret — значение заголовка X-Telegram-Bot-Api-Secret-Token.
	// Без него вебхук может дёрнуть кто угодно (§12.2).
	secret      string
	botUsername string
	log         *slog.Logger
}

func NewTelegramHandler(svc *Service, sender *TelegramSender, secret, botUsername string, log *slog.Logger) *TelegramHandler {
	if log == nil {
		log = slog.Default()
	}
	return &TelegramHandler{svc: svc, sender: sender, secret: secret, botUsername: botUsername, log: log}
}

// Routes подключает привязку: она требует входа в кабинет.
func (h *TelegramHandler) Routes(r chi.Router) {
	r.Post("/telegram/link", h.handleLink)
}

// PublicRoutes подключает вебхук: его вызывает Telegram, а не браузер.
func (h *TelegramHandler) PublicRoutes(r chi.Router) {
	r.Post("/telegram/webhook", h.handleWebhook)
}

func (h *TelegramHandler) handleLink(w http.ResponseWriter, r *http.Request) {
	principal := auth.MustFromContext(r.Context())

	if principal.Tenant.IsSandbox {
		httpx.Error(w, r, httpx.Forbidden("В демо внешние каналы отключены."))
		return
	}
	if h.botUsername == "" {
		httpx.Error(w, r, httpx.New(http.StatusServiceUnavailable, "telegram-unconfigured",
			"Telegram не настроен", "Бот не подключён к этому серверу."))
		return
	}

	invite, err := h.svc.CreateLinkInvite(r.Context(), principal.Tenant, principal.UserID, h.botUsername)
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	httpx.JSON(w, http.StatusOK, invite)
}

// update — минимальная часть Update из Bot API, которая нам нужна.
type update struct {
	Message *struct {
		Chat struct {
			ID int64 `json:"id"`
		} `json:"chat"`
		Text string `json:"text"`
		From struct {
			FirstName string `json:"first_name"`
		} `json:"from"`
	} `json:"message"`
}

func (h *TelegramHandler) handleWebhook(w http.ResponseWriter, r *http.Request) {
	// Секрет в заголовке — единственное, что отличает Telegram от постороннего.
	got := r.Header.Get("X-Telegram-Bot-Api-Secret-Token")
	if h.secret == "" || subtle.ConstantTimeCompare([]byte(got), []byte(h.secret)) != 1 {
		// Подробностей не выдаём: чем меньше знает чужой, тем лучше.
		w.WriteHeader(http.StatusForbidden)
		return
	}

	var u update
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(&u); err != nil {
		// Telegram повторяет неудачные доставки; на мусор отвечаем 200,
		// чтобы он не долбился бесконечно.
		w.WriteHeader(http.StatusOK)
		return
	}
	if u.Message == nil {
		w.WriteHeader(http.StatusOK)
		return
	}

	chatID := u.Message.Chat.ID
	text := strings.TrimSpace(u.Message.Text)

	// Отвечаем всегда 200: ошибка обработки — наша проблема, а не Telegram.
	defer w.WriteHeader(http.StatusOK)

	switch {
	case strings.HasPrefix(text, "/start"):
		h.handleStart(r.Context(), chatID, strings.TrimSpace(strings.TrimPrefix(text, "/start")))
	case text == "/stop":
		h.handleStop(r.Context(), chatID)
	default:
		h.reply(r.Context(), chatID,
			"Я присылаю сводку по складу и срочные алерты.\n\n"+
				"Чтобы привязать чат, откройте настройки в кабинете Zapas "+
				"и нажмите «Привязать Telegram».\n"+
				"Отвязать — командой /stop.")
	}
}

func (h *TelegramHandler) handleStart(ctx context.Context, chatID int64, code string) {
	if code == "" {
		h.reply(ctx, chatID,
			"Чтобы получать сводку, привяжите чат: откройте настройки в кабинете "+
				"Zapas и нажмите «Привязать Telegram». Ссылка действует 15 минут.")
		return
	}

	name, err := h.svc.LinkChat(ctx, code, chatID)
	switch {
	case err == nil:
		greeting := "Готово"
		if name != "" {
			greeting = "Готово, " + name
		}
		h.reply(ctx, chatID, greeting+"! Теперь сводка и срочные алерты приходят сюда.\n\n"+
			"Сводка — каждое утро, алерты — сразу, но в тихие часы откладываются до утра.\n"+
			"Отвязать чат: /stop")
	case errors.Is(err, ErrLinkCodeInvalid):
		h.reply(ctx, chatID,
			"Ссылка не подошла — скорее всего, истекли 15 минут. "+
				"Откройте настройки в кабинете и получите новую.")
	default:
		h.log.ErrorContext(ctx, "не удалось привязать чат",
			slog.Int64("chat_id", chatID), slog.String("err", err.Error()))
		h.reply(ctx, chatID, "Что-то пошло не так. Попробуйте ещё раз через минуту.")
	}
}

func (h *TelegramHandler) handleStop(ctx context.Context, chatID int64) {
	if err := h.svc.UnlinkChat(ctx, chatID); err != nil {
		h.log.ErrorContext(ctx, "не удалось отвязать чат",
			slog.Int64("chat_id", chatID), slog.String("err", err.Error()))
		h.reply(ctx, chatID, "Не получилось отвязать. Попробуйте ещё раз.")
		return
	}
	h.reply(ctx, chatID, "Чат отвязан — сюда больше ничего не придёт. "+
		"Привязать снова можно в настройках кабинета.")
}

func (h *TelegramHandler) reply(ctx context.Context, chatID int64, text string) {
	if h.sender == nil || !h.sender.Configured() {
		return
	}
	err := h.sender.Send(ctx, itoa(chatID), Payload{Body: text})
	if err != nil {
		h.log.ErrorContext(ctx, "не удалось ответить в Telegram",
			slog.Int64("chat_id", chatID), slog.String("err", err.Error()))
	}
}

func itoa(v int64) string { return strconv.FormatInt(v, 10) }
