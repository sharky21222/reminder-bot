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
		log.Fatal("❌ Ошибка запуска бота:", err)
	}

	// Health-check
	http.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(200)
		w.Write([]byte("✅ OK"))
	})
	go http.ListenAndServe(":8081", nil)

	u := tgbotapi.NewUpdate(0)
	u.Timeout = 60
	updates := bot.GetUpdatesChan(u)

	menu := tgbotapi.NewReplyKeyboard(
		tgbotapi.NewKeyboardButtonRow(
			tgbotapi.NewKeyboardButton("📝 Напомни мне..."),
			tgbotapi.NewKeyboardButton("❓ Помощь"),
		),
	)

	for upd := range updates {
		if upd.Message == nil {
			continue
		}
		msgText := strings.ToLower(upd.Message.Text)

		switch {
		case msgText == "/start":
			msg := tgbotapi.NewMessage(upd.Message.Chat.ID, "👋 Привет! Я помогу тебе с напоминаниями.")
			msg.ReplyMarkup = menu
			bot.Send(msg)

		case msgText == "/help", strings.Contains(msgText, "помощь"):
			help := "📚 Команды:\n" +
				"/remind <время> <текст> — пример: /remind 10s выйти\n" +
				"Напомни мне через <время> <текст> — пример: Напомни через 2 минуты выпить чай"
			bot.Send(tgbotapi.NewMessage(upd.Message.Chat.ID, help))

		case msgText == "/menu":
			msg := tgbotapi.NewMessage(upd.Message.Chat.ID, "📋 Главное меню:")
			msg.ReplyMarkup = menu
			bot.Send(msg)

		case strings.HasPrefix(msgText, "/remind"):
			handleRemind(bot, upd, msgText)

		case strings.Contains(msgText, "напомни") && strings.Contains(msgText, "через"):
			parseNatural(bot, upd, msgText)

		default:
			bot.Send(tgbotapi.NewMessage(upd.Message.Chat.ID, "🤖 Не понял. Нажми /help для списка команд."))
		}
	}
}

func handleRemind(bot *tgbotapi.BotAPI, upd tgbotapi.Update, text string) {
	parts := strings.SplitN(text, " ", 3)
	if len(parts) < 3 {
		bot.Send(tgbotapi.NewMessage(upd.Message.Chat.ID, "⚠️ /remind <время> <текст>"))
		return
	}
	d, err := time.ParseDuration(parts[1])
	if err != nil {
		bot.Send(tgbotapi.NewMessage(upd.Message.Chat.ID, "❌ Время неверно. Пример: 10s, 2m, 1h"))
		return
	}
	bot.Send(tgbotapi.NewMessage(upd.Message.Chat.ID, "⏳ Ок, напомню через "+parts[1]))
	go func(id int64, delay time.Duration, msg string) {
		time.Sleep(delay)
		bot.Send(tgbotapi.NewMessage(id, "🔔 Напоминание: "+msg))
	}(upd.Message.Chat.ID, d, parts[2])
}

func parseNatural(bot *tgbotapi.BotAPI, upd tgbotapi.Update, text string) {
	// Пример: напомни мне через 2 минуты попить
	r := regexp.MustCompile(`через (\d+)\s*(секунд[ы]?|сек|s|минут[ы]?|мин|m|час[аов]*|ч|h)\s*(.*)?`)
	m := r.FindStringSubmatch(text)
	if len(m) < 4 {
		bot.Send(tgbotapi.NewMessage(upd.Message.Chat.ID, "⚠️ Не понял команду. Пример: Напомни через 10 секунд сделать что-то"))
		return
	}

	value := m[1]
	unit := strings.ToLower(m[2])
	message := m[3]

	// Преобразуем в duration
	var duration time.Duration
	switch {
	case strings.HasPrefix(unit, "сек"):
		duration, _ = time.ParseDuration(value + "s")
	case strings.HasPrefix(unit, "мин"):
		duration, _ = time.ParseDuration(value + "m")
	case strings.HasPrefix(unit, "ч"):
		duration, _ = time.ParseDuration(value + "h")
	default:
		bot.Send(tgbotapi.NewMessage(upd.Message.Chat.ID, "❌ Неподдерживаемая единица времени"))
		return
	}

	bot.Send(tgbotapi.NewMessage(upd.Message.Chat.ID, "🕐 Хорошо, напомню через "+value+" "+unit))
	go func(id int64, d time.Duration, msg string) {
		time.Sleep(d)
		bot.Send(tgbotapi.NewMessage(id, "🔔 Напоминание: "+msg))
	}(upd.Message.Chat.ID, duration, message)
}
