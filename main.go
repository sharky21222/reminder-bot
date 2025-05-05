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

	bot, err := tgbotapi.NewBotAPI(token)
	if err != nil {
		log.Fatal(err)
	}

	// Health‑check
	http.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(200)
		w.Write([]byte("OK"))
	})
	go http.ListenAndServe(":8081", nil)

	// Клавиатура
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
		// Старт
		case text == "/start":
			msg := tgbotapi.NewMessage(upd.Message.Chat.ID, "👋 Привет! Я бот-напоминалка.")
			msg.ReplyMarkup = menu
			bot.Send(msg)

		// Помощь
		case text == "/help" || text == "📖 помощь":
			help := "📚 Команды:\n" +
				"/remind <время> <текст> — пример: /remind 10s выйти\n" +
				"Напиши просто: через 5 сек пойти гулять\n" +
				"/menu — показать кнопки"
			bot.Send(tgbotapi.NewMessage(upd.Message.Chat.ID, help).SetReplyMarkup(menu))

		// Меню
		case text == "/menu":
			bot.Send(tgbotapi.NewMessage(upd.Message.Chat.ID, "📋 Меню:").SetReplyMarkup(menu))

		// Нажали кнопку "📝 Напомни мне..."
		case text == "📝 напомни мне...":
			bot.Send(tgbotapi.NewMessage(upd.Message.Chat.ID,
				"✍ Пожалуйста, введи: через 10s сделать что-то").SetReplyMarkup(menu))

		// /remind
		case strings.HasPrefix(text, "/remind"):
			if dur, note, ok := parseNatural(text); ok {
				schedule(bot, upd.Message.Chat.ID, dur, note)
			} else {
				bot.Send(tgbotapi.NewMessage(upd.Message.Chat.ID,
					"⛔ Формат: /remind 10s текст или: через 5m текст").SetReplyMarkup(menu))
			}

		// Естественный ввод: "через 5 сек ...", "через 2 минуты ..."
		case strings.HasPrefix(text, "через "):
			if dur, note, ok := parseNatural(text); ok {
				schedule(bot, upd.Message.Chat.ID, dur, note)
			} else {
				bot.Send(tgbotapi.NewMessage(upd.Message.Chat.ID,
					"⛔ Время неверно. Пример: через 10s текст").SetReplyMarkup(menu))
			}

		// Всё остальное
		default:
			bot.Send(tgbotapi.NewMessage(upd.Message.Chat.ID,
				"🤖 Не понял. Нажми /help для списка команд.").SetReplyMarkup(menu))
		}
	}
}

// schedule отправляет подтверждение и через duration шлёт напоминание
func schedule(bot *tgbotapi.BotAPI, chatID int64, d time.Duration, note string) {
	bot.Send(tgbotapi.NewMessage(chatID, "⏳ Ок, напомню через "+d.String()))
	go func() {
		time.Sleep(d)
		bot.Send(tgbotapi.NewMessage(chatID, "🔔 Напоминание: "+note))
	}()
}

// parseNatural распознаёт "через N [s/m/h]" или "/remind N[s/m/h] текст"
func parseNatural(text string) (time.Duration, string, bool) {
	// выдёргиваем число, единицу и текст
	r := regexp.MustCompile(`(\d+)\s*(s|секунд[ы]?|сек|m|минут[ы]?|мин|h|час[аов]?|ч)\s*(.*)`)
	m := r.FindStringSubmatch(text)
	if len(m) < 4 {
		return 0, "", false
	}
	num, unit, note := m[1], m[2], m[3]

	// нормализуем единицу
	var suf string
	switch {
	case strings.HasPrefix(unit, "s"), unit == "сек", strings.HasPrefix(unit, "секунд"):
		suf = "s"
	case unit == "m", strings.HasPrefix(unit, "мин"):
		suf = "m"
	case unit == "h", strings.HasPrefix(unit, "час"), unit == "ч":
		suf = "h"
	default:
		return 0, "", false
	}

	dur, err := time.ParseDuration(num + suf)
	if err != nil {
		return 0, "", false
	}
	return dur, note, true
}
