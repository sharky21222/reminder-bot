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
		if upd.Message != nil {
			msgText := upd.Message.Text

			switch {
			case msgText == "/start":
				bot.Send(tgbotapi.NewMessage(upd.Message.Chat.ID, "Привет! Я бот-напоминалка. Напиши /help"))
			case msgText == "/help":
				bot.Send(tgbotapi.NewMessage(upd.Message.Chat.ID, "Команды:\n/remind 10s что-то сделать\n/time — текущее время\n/menu — кнопки"))
			case msgText == "/time":
				bot.Send(tgbotapi.NewMessage(upd.Message.Chat.ID, "⏰ Сейчас "+time.Now().Format("15:04:05")))
			case strings.HasPrefix(msgText, "/remind"):
				parts := strings.SplitN(msgText, " ", 3)
				if len(parts) < 3 {
					bot.Send(tgbotapi.NewMessage(upd.Message.Chat.ID, "Формат: /remind 10s текст"))
					continue
				}
				d, err := time.ParseDuration(parts[1])
				if err != nil {
					bot.Send(tgbotapi.NewMessage(upd.Message.Chat.ID, "Время должно быть как 10s, 5m, 1h"))
					continue
				}
				msg := parts[2]
				bot.Send(tgbotapi.NewMessage(upd.Message.Chat.ID, "Ок, напомню через "+parts[1]))
				go func(id int64, delay time.Duration, text string) {
					time.Sleep(delay)
					bot.Send(tgbotapi.NewMessage(id, "🔔 Напоминание: "+text))
				}(upd.Message.Chat.ID, d, msg)
			case msgText == "/menu":
				msg := tgbotapi.NewMessage(upd.Message.Chat.ID, "Выберите опцию:")
				msg.ReplyMarkup = tgbotapi.NewInlineKeyboardMarkup(
					tgbotapi.NewInlineKeyboardRow(
						tgbotapi.NewInlineKeyboardButtonData("🕒 Время", "time"),
						tgbotapi.NewInlineKeyboardButtonData("❓ Помощь", "help"),
						tgbotapi.NewInlineKeyboardButtonData("🔐 Секрет", "secret"),
					),
				)
				bot.Send(msg)
			}
		}

		if upd.CallbackQuery != nil {
			var response string
			switch upd.CallbackQuery.Data {
			case "time":
				response = "⏰ Сейчас " + time.Now().Format("15:04:05")
			case "help":
				response = "Напиши: /remind 10s что-то сделать"
			case "secret":
				response = "🔐 Секрет: ты крутой!"
			}
			bot.Send(tgbotapi.NewMessage(upd.CallbackQuery.Message.Chat.ID, response))
			bot.Request(tgbotapi.NewCallback(upd.CallbackQuery.ID, ""))
		}
	}
}
