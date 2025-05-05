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

	// health‑check
	http.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("OK"))
	})
	go http.ListenAndServe(":8081", nil)

	// клавиатура
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
		text := strings.TrimSpace(strings.ToLower(upd.Message.Text))

		// любое сообщение сразу показывает меню
		if text != "" {
			msg := tgbotapi.NewMessage(upd.Message.Chat.ID, "📋 Меню:")
			msg.ReplyMarkup = menu
			bot.Send(msg)
		}

		// дальше обрабатываем специальные команды
		switch {
		case text == "/start":
			msg := tgbotapi.NewMessage(upd.Message.Chat.ID, "👋 Привет! Я бот-напоминалка.")
			msg.ReplyMarkup = menu
			bot.Send(msg)

		case text == "📝 напомни мне":
			msg := tgbotapi.NewMessage(upd.Message.Chat.ID, "✍ Введи, например: через 5 сек пойти гулять")
			msg.ReplyMarkup = menu
			bot.Send(msg)

		case text == "/help" || text == "📖 помощь":
			help := "📚 Команды:\n" +
				"/remind <время> <текст>\n" +
				"Например: через 5 сек пойти гулять\n" +
				"/menu — показать меню"
			msg := tgbotapi.NewMessage(upd.Message.Chat.ID, help)
			msg.ReplyMarkup = menu
			bot.Send(msg)

		case strings.HasPrefix(text, "через "):
			if dur, note, ok := parseNatural(text); ok {
				schedule(bot, upd.Message.Chat.ID, dur, note)
			}

		case strings.HasPrefix(text, "/remind"):
			parts := strings.SplitN(text, " ", 3)
			if len(parts) >= 3 {
				if dur, note, ok := parseNatural(parts[1] + " " + parts[2]); ok {
					schedule(bot, upd.Message.Chat.ID, dur, note)
				}
			}

			// больше не отправляем "не понял" — меню уже показано
		}
	}
}

func schedule(bot *tgbotapi.BotAPI, chatID int64, d time.Duration, note string) {
	confirm := tgbotapi.NewMessage(chatID, "⏳ Ок, напомню через "+d.String())
	bot.Send(confirm)
	go func() {
		time.Sleep(d)
		bot.Send(tgbotapi.NewMessage(chatID, "🔔 Напоминание: "+note))
	}()
}

var re = regexp.MustCompile(`через\s+(\d+)\s*(секунд[ы]?|сек|с|минут[ы]?|мин|m|час[аов]?|ч|h)\s*(.*)`)

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
