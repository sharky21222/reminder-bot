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
		log.Fatal("TELEGRAM_BOT_TOKEN не задан")
	}

	bot, err := tgbotapi.NewBotAPI(token)
	if err != nil {
		log.Fatal(err)
	}

	http.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(200)
		w.Write([]byte("OK"))
	})
	go http.ListenAndServe(":8081", nil)

	u := tgbotapi.NewUpdate(0)
	u.Timeout = 60
	updates := bot.GetUpdatesChan(u)

	for upd := range updates {
		if upd.Message == nil {
			continue
		}

		msg := upd.Message.Text
		chatID := upd.Message.Chat.ID

		switch {
		case msg == "/start":
			bot.Send(tgbotapi.NewMessage(chatID, "👋 Привет! Я бот-напоминалка. Напиши /help для инструкций."))
		case msg == "/help":
			bot.Send(tgbotapi.NewMessage(chatID, "Напиши: /remind <время> <текст>\nПример: /remind 10s Проверить почту"))
		case msg == "/time":
			now := time.Now().Format("02 Jan 2006 15:04:05")
			bot.Send(tgbotapi.NewMessage(chatID, "🕒 Время сервера: "+now))
		case strings.HasPrefix(msg, "/remind"):
			parts := strings.SplitN(msg, " ", 3)
			if len(parts) < 3 {
				bot.Send(tgbotapi.NewMessage(chatID, "Используй: /remind <время> <текст>"))
				continue
			}
			d, err := time.ParseDuration(parts[1])
			if err != nil {
				bot.Send(tgbotapi.NewMessage(chatID, "⛔ Неверный формат времени, например: 10s, 5m, 1h"))
				continue
			}
			note := parts[2]
			bot.Send(tgbotapi.NewMessage(chatID, "📝 Ок, напомню через "+parts[1]))

			go func(id int64, delay time.Duration, msg string) {
				time.Sleep(delay)
				bot.Send(tgbotapi.NewMessage(id, "⏰ Напоминание: "+msg))
			}(chatID, d, note)
		}
	}
}
