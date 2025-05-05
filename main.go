package main

import (
	"log"
	"net/http"
	"os"
	"regexp"
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

	// Health-check
	http.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(200)
		w.Write([]byte("OK"))
	})
	go http.ListenAndServe(":8081", nil)

	// Кнопки
	menu := tgbotapi.NewReplyKeyboard(
		tgbotapi.NewKeyboardButtonRow(
			tgbotapi.NewKeyboardButton("/start"),
			tgbotapi.NewKeyboardButton("/help"),
		),
	)

	// Обработка команд
	u := tgbotapi.NewUpdate(0)
	u.Timeout = 60
	updates := bot.GetUpdatesChan(u)

	re := regexp.MustCompile(`(?i)напомни.*через (\d+[smhd]) (.+)`)

	for upd := range updates {
		if upd.Message == nil {
			continue
		}

		msgText := upd.Message.Text

		// /start
		if msgText == "/start" {
			msg := tgbotapi.NewMessage(upd.Message.Chat.ID, "Привет! Я помогу тебе с напоминаниями.")
			msg.ReplyMarkup = menu
			bot.Send(msg)
			continue
		}

		// /help
		if msgText == "/help" {
			bot.Send(tgbotapi.NewMessage(upd.Message.Chat.ID,
				`Напиши сообщение вроде:
"напомни мне через 10s сделать зарядку"
"напомни через 5m позвонить другу"`))
			continue
		}

		// Обработка естественного запроса
		matches := re.FindStringSubmatch(msgText)
		if len(matches) == 3 {
			durationStr := matches[1]
			note := matches[2]

			d, err := time.ParseDuration(durationStr)
			if err != nil {
				bot.Send(tgbotapi.NewMessage(upd.Message.Chat.ID, "Ошибка формата времени (пример: 10s, 5m, 1h)"))
				continue
			}

			bot.Send(tgbotapi.NewMessage(upd.Message.Chat.ID, "Ок, напомню через "+durationStr))

			go func(id int64, delay time.Duration, msg string) {
				time.Sleep(delay)
				bot.Send(tgbotapi.NewMessage(id, "🔔 Напоминание: "+msg))
			}(upd.Message.Chat.ID, d, note)

		} else {
			bot.Send(tgbotapi.NewMessage(upd.Message.Chat.ID, "Не понял. Нажми /help для примера"))
		}
	}
}
