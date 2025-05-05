package main

import (
	"log"
	"net/http"
	"os"
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
			msg := tgbotapi.NewMessage(upd.Message.Chat.ID, "👋 Привет! Я помогу тебе с напоминаниями. Нажми кнопку или введи команду.")
			msg.ReplyMarkup = menu
			bot.Send(msg)

		case msgText == "/help", strings.Contains(msgText, "помощь"):
			help := "📚 Доступные команды:\n" +
				"/remind <время> <текст> — создать напоминание (например: /remind 10s позвонить)\n" +
				"Напомни мне <текст> через <время> — естественная команда (например: Напомни мне выйти через 5m)\n" +
				"/menu — показать кнопки"
			bot.Send(tgbotapi.NewMessage(upd.Message.Chat.ID, help))

		case msgText == "/menu":
			msg := tgbotapi.NewMessage(upd.Message.Chat.ID, "📋 Главное меню:")
			msg.ReplyMarkup = menu
			bot.Send(msg)

		case strings.HasPrefix(msgText, "/remind"):
			handleRemind(bot, upd, msgText)

		case strings.HasPrefix(msgText, "напомни мне"):
			parseNatural(bot, upd, msgText)

		default:
			bot.Send(tgbotapi.NewMessage(upd.Message.Chat.ID, "🤖 Не понял. Нажми /help для списка команд."))
		}
	}
}

func handleRemind(bot *tgbotapi.BotAPI, upd tgbotapi.Update, text string) {
	parts := strings.SplitN(text, " ", 3)
	if len(parts) < 3 {
		bot.Send(tgbotapi.NewMessage(upd.Message.Chat.ID,
			"⚠️ Используй: /remind <время> <текст>, например /remind 10s Сделать перерыв"))
		return
	}

	d, err := time.ParseDuration(parts[1])
	if err != nil {
		bot.Send(tgbotapi.NewMessage(upd.Message.Chat.ID,
			"❌ Неверный формат времени. Примеры: 10s, 5m, 1h"))
		return
	}

	note := parts[2]
	bot.Send(tgbotapi.NewMessage(upd.Message.Chat.ID, "⏳ Ок, напомню через "+parts[1]))

	go func(id int64, delay time.Duration, msg string) {
		time.Sleep(delay)
		bot.Send(tgbotapi.NewMessage(id, "⏰ Напоминание: "+msg))
	}(upd.Message.Chat.ID, d, note)
}

func parseNatural(bot *tgbotapi.BotAPI, upd tgbotapi.Update, text string) {
	if !strings.Contains(text, "через") {
		bot.Send(tgbotapi.NewMessage(upd.Message.Chat.ID, "❗ Укажи время через сколько напомнить, например: Напомни мне поесть через 30m"))
		return
	}

	parts := strings.SplitN(text, "через", 2)
	if len(parts) < 2 {
		bot.Send(tgbotapi.NewMessage(upd.Message.Chat.ID, "⚠️ Не смог разобрать команду"))
		return
	}

	message := strings.TrimSpace(parts[0][len("напомни мне"):])
	duration := strings.TrimSpace(parts[1])

	d, err := time.ParseDuration(duration)
	if err != nil {
		bot.Send(tgbotapi.NewMessage(upd.Message.Chat.ID, "⛔ Время указано неверно. Примеры: 10s, 5m, 1h"))
		return
	}

	bot.Send(tgbotapi.NewMessage(upd.Message.Chat.ID, "⏳ Ок, напомню через "+duration))

	go func(id int64, delay time.Duration, msg string) {
		time.Sleep(delay)
		bot.Send(tgbotapi.NewMessage(id, "🔔 Напоминание: "+msg))
	}(upd.Message.Chat.ID, d, message)
}
