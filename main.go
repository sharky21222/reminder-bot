package main

import (
	"fmt"
	"log"
	"net/http"
	"os"
	"regexp"
	"strings"
	"sync"
	"time"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
)

type Reminder struct {
	ID     string // уникальный идентификатор
	ChatID int64
	Note   string
	At     time.Time
}

var (
	re        = regexp.MustCompile(`(\d+)\s*(секунд[ы]?|сек|с|минут[ы]?|мин|m|час[аов]?|ч|h)\s*(.*)`)
	reminders = make([]Reminder, 0)
	mu        sync.Mutex
)

func main() {
	token := os.Getenv("TELEGRAM_BOT_TOKEN")
	if token == "" {
		log.Fatal("🚫 TELEGRAM_BOT_TOKEN не задан")
	}
	bot, err := tgbotapi.NewBotAPI(token)
	if err != nil {
		log.Fatal(err)
	}

	// health‑check
	http.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("OK"))
	})
	go http.ListenAndServe(":8081", nil)

	menu := tgbotapi.NewReplyKeyboard(
		tgbotapi.NewKeyboardButtonRow(
			tgbotapi.NewKeyboardButton("📝 Напомни мне"),
			tgbotapi.NewKeyboardButton("📋 Список"),
		),
	)

	u := tgbotapi.NewUpdate(0)
	u.Timeout = 60
	updates := bot.GetUpdatesChan(u)

	for upd := range updates {
		// Обработка callback‑запроса от inline‑кнопки
		if upd.CallbackQuery != nil {
			handleCallback(bot, upd.CallbackQuery)
			continue
		}

		if upd.Message == nil {
			continue
		}
		text := strings.TrimSpace(strings.ToLower(upd.Message.Text))

		if text == "/start" || strings.Contains(text, "привет") {
			msg := tgbotapi.NewMessage(upd.Message.Chat.ID, "👋 Привет! Я бот‑напоминалка.")
			msg.ReplyMarkup = menu
			bot.Send(msg)
			continue
		}

		switch {
		case text == "📝 напомни мне":
			msg := tgbotapi.NewMessage(upd.Message.Chat.ID,
				"✍ Введите, например:\nнапомни через 5 сек пойти гулять")
			msg.ReplyMarkup = menu
			bot.Send(msg)

		case text == "📋 список":
			showList(bot, upd.Message.Chat.ID)

		case text == "/help":
			bot.Send(tgbotapi.NewMessage(upd.Message.Chat.ID,
				"📚 Команды:\n"+
					"/remind <время> <текст>\n"+
					"Например: напомни через 5 сек пойти гулять\n"+
					"📝 Напомни мне — подсказка\n"+
					"📋 Список — активные напоминания"))

		default:
			if dur, note, ok := parseAny(text); ok {
				schedule(bot, upd.Message.Chat.ID, dur, note)
			}
		}
	}
}

func schedule(bot *tgbotapi.BotAPI, chatID int64, d time.Duration, note string) {
	at := time.Now().Add(d)
	id := fmt.Sprintf("%d_%d", chatID, at.UnixNano())
	mu.Lock()
	reminders = append(reminders, Reminder{ID: id, ChatID: chatID, Note: note, At: at})
	mu.Unlock()

	bot.Send(tgbotapi.NewMessage(chatID, "⏳ Ок, напомню через "+d.String()))
	go func() {
		time.Sleep(d)
		bot.Send(tgbotapi.NewMessage(chatID, "🔔 Напоминание: "+note))
		removeByID(id)
	}()
}

func showList(bot *tgbotapi.BotAPI, chatID int64) {
	mu.Lock()
	defer mu.Unlock()

	if len(reminders) == 0 {
		bot.Send(tgbotapi.NewMessage(chatID, "📋 Нет активных напоминаний"))
		return
	}

	for _, r := range reminders {
		if r.ChatID != chatID {
			continue
		}
		remaining := time.Until(r.At).Truncate(time.Second)
		text := fmt.Sprintf("• %s (через %s)", r.Note, remaining)

		// inline‑кнопка удаления
		btn := tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData("❌ Удалить", r.ID),
		)
		msg := tgbotapi.NewMessage(chatID, text)
		msg.ReplyMarkup = tgbotapi.NewInlineKeyboardMarkup(btn)
		bot.Send(msg)
	}
}

func handleCallback(bot *tgbotapi.BotAPI, cq *tgbotapi.CallbackQuery) {
	id := cq.Data
	removeByID(id)
	bot.AnswerCallbackQuery(tgbotapi.NewCallback(cq.ID, "Удалено"))
	bot.Send(tgbotapi.NewMessage(cq.Message.Chat.ID, "✅ Напоминание удалено"))
}

func removeByID(id string) {
	mu.Lock()
	defer mu.Unlock()
	for i, r := range reminders {
		if r.ID == id {
			reminders = append(reminders[:i], reminders[i+1:]...)
			return
		}
	}
}

func parseAny(text string) (time.Duration, string, bool) {
	text = strings.TrimPrefix(text, "/remind ")
	text = strings.TrimPrefix(text, "напомни ")
	m := re.FindStringSubmatch(text)
	if len(m) != 4 {
		return 0, "", false
	}
	num, unit, note := m[1], m[2], m[3]
	var suf string
	switch {
	case strings.HasPrefix(unit, "сек"), unit == "с":
		suf = "s"
	case strings.HasPrefix(unit, "мин"), unit == "m":
		suf = "m"
	case strings.HasPrefix(unit, "час"), unit == "h", unit == "ч":
		suf = "h"
	default:
		return 0, "", false
	}
	d, err := time.ParseDuration(num + suf)
	if err != nil {
		return 0, "", false
	}
	return d, note, true
}
