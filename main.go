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
	ChatID int64
	Note   string
	At     time.Time
}

var (
	// Парсим любую фразу вида "число + единица + текст"
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

	// Health‑check
	http.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("OK"))
	})
	go http.ListenAndServe(":8081", nil)

	// Клавиатура с двумя кнопками
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
		if upd.Message == nil {
			continue
		}
		text := strings.TrimSpace(strings.ToLower(upd.Message.Text))

		// /start или "привет" → показать меню
		if text == "/start" || strings.Contains(text, "привет") {
			msg := tgbotapi.NewMessage(upd.Message.Chat.ID, "👋 Привет! Я бот‑напоминалка.")
			msg.ReplyMarkup = menu
			bot.Send(msg)
			continue
		}

		// Обработка кнопок и команд
		switch {
		// Кнопка "📝 Напомни мне"
		case text == "📝 напомни мне":
			msg := tgbotapi.NewMessage(upd.Message.Chat.ID,
				"✍ Введите, например:\nнапомни через 5 сек пойти гулять")
			msg.ReplyMarkup = menu
			bot.Send(msg)

		// Кнопка "📋 Список"
		case text == "📋 список":
			sendList(bot, upd.Message.Chat.ID)

		// /help
		case text == "/help":
			help := "📚 Команды:\n" +
				"/remind <время> <текст>\n" +
				"Например: напомни через 5 сек пойти гулять\n" +
				"📝 Напомни мне — подсказка\n" +
				"📋 Список — активные напоминания"
			bot.Send(tgbotapi.NewMessage(upd.Message.Chat.ID, help))

		// Универсальный парсинг любой фразы с временем
		default:
			if dur, note, ok := parseAny(text); ok {
				schedule(bot, upd.Message.Chat.ID, dur, note)
			}
			// иначе молчим — меню уже видно после /start
		}
	}
}

func schedule(bot *tgbotapi.BotAPI, chatID int64, d time.Duration, note string) {
	at := time.Now().Add(d)
	mu.Lock()
	reminders = append(reminders, Reminder{ChatID: chatID, Note: note, At: at})
	mu.Unlock()

	bot.Send(tgbotapi.NewMessage(chatID, "⏳ Ок, напомню через "+d.String()))
	go func() {
		time.Sleep(d)
		bot.Send(tgbotapi.NewMessage(chatID, "🔔 Напоминание: "+note))
		// удалить отработавшее
		mu.Lock()
		defer mu.Unlock()
		for i, r := range reminders {
			if r.ChatID == chatID && r.Note == note && r.At.Equal(at) {
				reminders = append(reminders[:i], reminders[i+1:]...)
				break
			}
		}
	}()
}

func sendList(bot *tgbotapi.BotAPI, chatID int64) {
	mu.Lock()
	defer mu.Unlock()
	var lines []string
	for _, r := range reminders {
		if r.ChatID == chatID {
			remaining := time.Until(r.At).Truncate(time.Second)
			lines = append(lines, fmt.Sprintf("• %s (через %s)", r.Note, remaining))
		}
	}
	if len(lines) == 0 {
		bot.Send(tgbotapi.NewMessage(chatID, "📋 Нет активных напоминаний"))
	} else {
		bot.Send(tgbotapi.NewMessage(chatID, "📋 Активные напоминания:\n"+strings.Join(lines, "\n")))
	}
}

// parseAny ловит и "/remind 5s ...", и "напомни через 5 сек ...", и "через 5m ..."
func parseAny(text string) (time.Duration, string, bool) {
	// убрать префиксы
	text = strings.TrimPrefix(text, "/remind ")
	text = strings.TrimPrefix(text, "напомни ")
	// найти число, единицу, заметку
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
