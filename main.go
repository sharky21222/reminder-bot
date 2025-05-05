package main

import (
	"log"
	"net/http"
	"os"
	"regexp"
	"strings"
	"time"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
)

func main() {
	token := os.Getenv("TELEGRAM_BOT_TOKEN")
	if token == "" {
		log.Fatal("TELEGRAM_BOT_TOKEN не задан")
	}

	bot, err := tgbotapi.NewBotAPI(token)
	if err != nil {
		log.Fatal(err)
	}

	// Health check
	http.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(200)
		w.Write([]byte("OK"))
	})
	go http.ListenAndServe(":8081", nil)

	// Кнопки
	menu := tgbotapi.NewReplyKeyboard(
		tgbotapi.NewKeyboardButtonRow(
			tgbotapi.NewKeyboardButton("📝 Напомни мне..."),
			tgbotapi.NewKeyboardButton("📖 Помощь"),
		),
	)

	u := tgbotapi.NewUpdate(0)
	u.Timeout = 60
	updates := bot.GetUpdatesChan(u)

	for upd := range updates {
		if upd.Message == nil {
			continue
		}

		text := strings.ToLower(upd.Message.Text)

		switch {
		case text == "/start":
			bot.Send(tgbotapi.NewMessage(upd.Message.Chat.ID, "👋 Привет! Я бот для напоминаний. Напиши, что тебе напомнить.").SetReplyMarkup(menu))

		case text == "/help" || text == "📖 помощь":
			bot.Send(tgbotapi.NewMessage(upd.Message.Chat.ID,
				`🛠 Примеры команд:
👉 /remind 10s Сделать задание
👉 напомни мне через 5 минут проверить комп
👉 /menu — показать кнопки`).SetReplyMarkup(menu))

		case text == "/menu":
			bot.Send(tgbotapi.NewMessage(upd.Message.Chat.ID, "📋 Меню открыто").SetReplyMarkup(menu))

		case text == "📝 напомни мне...":
			bot.Send(tgbotapi.NewMessage(upd.Message.Chat.ID, "✍ Напиши: \"напомни мне через 10 минут сделать задание\""))

		case strings.HasPrefix(text, "/remind") || strings.HasPrefix(text, "напомни мне через"):
			dur, msg, ok := parseNatural(text)
			if !ok {
				bot.Send(tgbotapi.NewMessage(upd.Message.Chat.ID, "⛔ Время указано неверно. Примеры: 10s, 5m, 1h"))
				continue
			}

			bot.Send(tgbotapi.NewMessage(upd.Message.Chat.ID, "✅ Ок, напомню через "+dur.String()))

			go func(id int64, d time.Duration, note string) {
				time.Sleep(d)
				bot.Send(tgbotapi.NewMessage(id, "🔔 Напоминание: "+note))
			}(upd.Message.Chat.ID, dur, msg)

		default:
			bot.Send(tgbotapi.NewMessage(upd.Message.Chat.ID, "🤖 Не понял. Нажми /help для списка команд.").SetReplyMarkup(menu))
		}
	}
}

// parseNatural распознаёт текст вида "напомни мне через 5 сек что-то"
func parseNatural(text string) (time.Duration, string, bool) {
	r := regexp.MustCompile(`через (\d+)\s?(секунд|сек|с|минут|мин|m|h|час|ч)\s?(.*)`)
	match := r.FindStringSubmatch(text)
	if len(match) < 4 {
		return 0, "", false
	}

	num := match[1]
	unit := match[2]
	note := match[3]
	var suffix string

	switch unit {
	case "секунд", "сек", "с":
		suffix = "s"
	case "минут", "мин", "m":
		suffix = "m"
	case "час", "ч", "h":
		suffix = "h"
	default:
		return 0, "", false
	}

	d, err := time.ParseDuration(num + suffix)
	if err != nil {
		return 0, "", false
	}
	return d, note, true
}
