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
	// Читаем токен из окружения
	token := os.Getenv("TELEGRAM_BOT_TOKEN")
	if token == "" {
		log.Fatal("TELEGRAM_BOT_TOKEN не задан")
	}

	bot, err := tgbotapi.NewBotAPI(token)
	if err != nil {
		log.Fatal(err)
	}

	// Health-check для внешних пингов
	http.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(200)
		w.Write([]byte("OK"))
	})
	go http.ListenAndServe(":8081", nil)

	// Основная логика бота
	u := tgbotapi.NewUpdate(0)
	u.Timeout = 60
	updates := bot.GetUpdatesChan(u)

	for upd := range updates {
		if upd.Message == nil || !strings.HasPrefix(upd.Message.Text, "/remind") {
			continue
		}
		parts := strings.SplitN(upd.Message.Text, " ", 3)
		if len(parts) < 3 {
			bot.Send(tgbotapi.NewMessage(upd.Message.Chat.ID,
				"Используй: /remind <duration> <текст>, например /remind 10s Проверить код"))
			continue
		}

		d, err := time.ParseDuration(parts[1])
		if err != nil {
			bot.Send(tgbotapi.NewMessage(upd.Message.Chat.ID,
				"Неверный формат времени, допустимо 10s, 5m, 1h"))
			continue
		}
		note := parts[2]
		bot.Send(tgbotapi.NewMessage(upd.Message.Chat.ID,
			"Ок, напомню через "+parts[1]))

		go func(id int64, delay time.Duration, msg string) {
			time.Sleep(delay)
			bot.Send(tgbotapi.NewMessage(id, "⏰ Напоминание: "+msg))
		}(upd.Message.Chat.ID, d, note)
	}
}
