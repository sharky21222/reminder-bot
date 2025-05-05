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
		log.Fatal("🚫 TELEGRAM_BOT_TOKEN не задан")
	}

	bot, _ := tgbotapi.NewBotAPI(token)

	// healthz
	http.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("OK"))
	})
	go http.ListenAndServe(":8081", nil)

	// клавиатура — убрали "..." в кнопке
	menu := tgbotapi.NewReplyKeyboard(
		tgbotapi.NewKeyboardButtonRow(
			tgbotapi.NewKeyboardButton("📝 Напомни мне"),
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
		// убираем пробелы и приводим к lower
		text := strings.TrimSpace(strings.ToLower(upd.Message.Text))

		// /start всегда показывает меню
		if text == "/start" {
			msg := tgbotapi.NewMessage(upd.Message.Chat.ID, "👋 Привет! Я бот-напоминалка.")
			msg.ReplyMarkup = menu
			bot.Send(msg)
			continue
		}

		// кнопка "📝 Напомни мне"
		if text == "📝 напомни мне" {
			msg := tgbotapi.NewMessage(upd.Message.Chat.ID,
				"✍ Введи, например: через 5 сек выйти на улицу")
			msg.ReplyMarkup = menu
			bot.Send(msg)
			continue
		}

		// /help или кнопка помощь
		if text == "/help" || text == "📖 помощь" {
			help := "📚 Команды:\n" +
				"/remind <время> <текст>\n" +
				"Например: напомни мне через 5 сек выйти на улицу\n" +
				"/menu — показать меню"
			bot.Send(tgbotapi.NewMessage(upd.Message.Chat.ID, help).SetReplyMarkup(menu))
			continue
		}

		// /menu
		if text == "/menu" {
			bot.Send(tgbotapi.NewMessage(upd.Message.Chat.ID, "📋 Меню").SetReplyMarkup(menu))
			continue
		}

		// если начинается с "через "
		if strings.HasPrefix(text, "через ") {
			if dur, note, ok := parseNatural(text); ok {
				schedule(bot, upd.Message.Chat.ID, dur, note)
			} else {
				bot.Send(tgbotapi.NewMessage(upd.Message.Chat.ID,
					"⛔ Формат: через 5 сек текст").SetReplyMarkup(menu))
			}
			continue
		}

		// /remind
		if strings.HasPrefix(text, "/remind") {
			if dur, note, ok := parseNatural(text); ok {
				schedule(bot, upd.Message.Chat.ID, dur, note)
			} else {
				bot.Send(tgbotapi.NewMessage(upd.Message.Chat.ID,
					"⛔ Формат: /remind 10s текст").SetReplyMarkup(menu))
			}
			continue
		}

		// всё остальное
		bot.Send(tgbotapi.NewMessage(upd.Message.Chat.ID,
			"🤖 Не понял. Нажми /help").SetReplyMarkup(menu))
	}
}

func schedule(bot *tgbotapi.BotAPI, chatID int64, d time.Duration, note string) {
	bot.Send(tgbotapi.NewMessage(chatID, "⏳ Ок, напомню через "+d.String()))
	go func() {
		time.Sleep(d)
		bot.Send(tgbotapi.NewMessage(chatID, "🔔 Напоминание: "+note))
	}()
}

var re = regexp.MustCompile(`(\d+)\s*(секунд[ы]?|сек|с|минут[ы]?|мин|m|час[аов]?|ч|h)\s*(.*)`)

func parseNatural(text string) (time.Duration, string, bool) {
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
