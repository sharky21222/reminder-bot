package main

import (
	"fmt"
	"log"
	"net/http"
	"os"
	"regexp"
	"strings"
	"sync"
	"time"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
)

type Reminder struct {
	ID         string
	ChatID     int64
	Note       string
	At         time.Time
	Category   string
	NeedRepeat bool
}

var (
	re          = regexp.MustCompile(`(\d+)\s*(секунд[ы]?|сек|с|минут[ы]?|мин|m|час[аов]?|ч|h)`)
	wordRe      = regexp.MustCompile(`\p{L}+`)
	reminders   = make([]Reminder, 0)
	timers      = make(map[string]*time.Timer)
	pendingNote = make(map[int64]string)
	repeatFlag  = make(map[int64]bool)
	mu          sync.Mutex
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

	http.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("OK"))
	})
	go http.ListenAndServe(":8081", nil)

	menu := tgbotapi.NewReplyKeyboard(
		tgbotapi.NewKeyboardButtonRow(
			tgbotapi.NewKeyboardButton("📝 Напомни мне"),
			tgbotapi.NewKeyboardButton("📋 Список"),
		),
		tgbotapi.NewKeyboardButtonRow(
			tgbotapi.NewKeyboardButton("🔁 Повтор включён"),
			tgbotapi.NewKeyboardButton("🔁 Повтор выключен"),
		),
	)

	u := tgbotapi.NewUpdate(0)
	u.Timeout = 60
	updates := bot.GetUpdatesChan(u)

	for upd := range updates {
		if upd.CallbackQuery != nil {
			handleCallback(bot, upd.CallbackQuery)
			continue
		}
		if upd.Message == nil {
			continue
		}
		chatID := upd.Message.Chat.ID
		text := strings.TrimSpace(strings.ToLower(upd.Message.Text))

		switch text {
		case "/start", "привет":
			msg := tgbotapi.NewMessage(chatID, "👋 Напиши напоминание, потом укажи время (например: через 5 сек пойти гулять).")
			msg.ReplyMarkup = menu
			bot.Send(msg)

		case "📝 напомни мне":
			bot.Send(tgbotapi.NewMessage(chatID, "✍ Что напомнить?"))

		case "📋 список":
			showList(bot, chatID)

		case "/help":
			bot.Send(tgbotapi.NewMessage(chatID,
				"📚 Напиши что напомнить — бот спросит через сколько.\n"+
					"📝 Напомни мне — диалог\n📋 Список — напоминания\n🔁 Повтор — включить/выключить повтор"))

		case "🔁 повтор включен":
			repeatFlag[chatID] = true
			bot.Send(tgbotapi.NewMessage(chatID, "🔁 Повтор включён"))

		case "🔁 повтор выключен":
			repeatFlag[chatID] = false
			bot.Send(tgbotapi.NewMessage(chatID, "🔁 Повтор выключен"))

		default:
			if note, ok := pendingNote[chatID]; ok {
				if m := re.FindStringSubmatch(text); len(m) == 3 {
					d, err := time.ParseDuration(m[1] + unitSuffix(m[2]))
					if err == nil {
						delete(pendingNote, chatID)
						schedule(bot, chatID, d, note)
						continue
					}
				}
				bot.Send(tgbotapi.NewMessage(chatID, "⛔ Неверный формат времени. Пример: 10s, 5m, 1h"))
				continue
			}
			pendingNote[chatID] = upd.Message.Text
			bot.Send(tgbotapi.NewMessage(chatID, "⏳ Через сколько напомнить?"))
		}
	}
}

func schedule(bot *tgbotapi.BotAPI, chatID int64, d time.Duration, note string) {
	at := time.Now().Add(d)
	id := fmt.Sprintf("%d_%d", chatID, at.UnixNano())
	category := classify(note)
	repeat := repeatFlag[chatID]

	mu.Lock()
	reminders = append(reminders, Reminder{
		ID:         id,
		ChatID:     chatID,
		Note:       note,
		At:         at,
		Category:   category,
		NeedRepeat: repeat,
	})
	mu.Unlock()

	timer := time.AfterFunc(d, func() {
		msg := tgbotapi.NewMessage(chatID, "🔔 Напоминание: "+note)
		msg.ReplyMarkup = tgbotapi.NewInlineKeyboardMarkup(
			tgbotapi.NewInlineKeyboardRow(
				tgbotapi.NewInlineKeyboardButtonData("✅ Выполнено", id),
			),
		)
		bot.Send(msg)

		if repeat {
			// повтор через 1 минуту, если не выполнено
			timers[id] = time.AfterFunc(1*time.Minute, func() {
				if stillExists(id) {
					bot.Send(tgbotapi.NewMessage(chatID, "🔁 Повтор: "+note))
				}
			})
		}
	})
	timers[id] = timer

	bot.Send(tgbotapi.NewMessage(chatID,
		fmt.Sprintf("✅ Запомнил! Напомню через %s (Категория: %s)", d.String(), category)))
}

func stillExists(id string) bool {
	mu.Lock()
	defer mu.Unlock()
	for _, r := range reminders {
		if r.ID == id {
			return true
		}
	}
	return false
}

func showList(bot *tgbotapi.BotAPI, chatID int64) {
	mu.Lock()
	defer mu.Unlock()

	grouped := map[string][]Reminder{}
	for _, r := range reminders {
		if r.ChatID == chatID {
			grouped[r.Category] = append(grouped[r.Category], r)
		}
	}
	if len(grouped) == 0 {
		bot.Send(tgbotapi.NewMessage(chatID, "📋 Нет активных напоминаний"))
		return
	}

	var rows [][]tgbotapi.InlineKeyboardButton
	var text strings.Builder
	for cat, items := range grouped {
		text.WriteString(fmt.Sprintf("🔖 *%s*:\n", cat))
		for _, r := range items {
			text.WriteString(fmt.Sprintf("• %s (через %s)\n", r.Note, time.Until(r.At).Truncate(time.Second)))
			rows = append(rows, tgbotapi.NewInlineKeyboardRow(
				tgbotapi.NewInlineKeyboardButtonData("❌ Удалить", r.ID),
			))
		}
		text.WriteString("\n")
	}

	msg := tgbotapi.NewMessage(chatID, text.String())
	msg.ParseMode = "Markdown"
	msg.ReplyMarkup = tgbotapi.NewInlineKeyboardMarkup(rows...)
	bot.Send(msg)
}

func handleCallback(bot *tgbotapi.BotAPI, cq *tgbotapi.CallbackQuery) {
	id := cq.Data

	mu.Lock()
	if t, ok := timers[id]; ok {
		t.Stop()
		delete(timers, id)
	}
	removeByID(id)
	mu.Unlock()

	callback := tgbotapi.NewCallback(cq.ID, "✅ Выполнено")
	bot.Request(callback)
}

func removeByID(id string) {
	for i, r := range reminders {
		if r.ID == id {
			reminders = append(reminders[:i], reminders[i+1:]...)
			break
		}
	}
}

func classify(text string) string {
	switch {
	case containsRoot(text, "код", "проект", "встреч", "дедлайн"):
		return "Работа"
	case containsRoot(text, "лекц", "дз", "экзамен", "школ"):
		return "Учёба"
	case containsRoot(text, "врач", "лекарств", "здоров"):
		return "Здоровье"
	default:
		return "Другое"
	}
}

func containsRoot(text string, roots ...string) bool {
	words := wordRe.FindAllString(strings.ToLower(text), -1)
	for _, w := range words {
		for _, root := range roots {
			if strings.HasPrefix(w, root) || strings.HasPrefix(root, w) {
				return true
			}
		}
	}
	return false
}

func unitSuffix(u string) string {
	u = strings.ToLower(u)
	switch {
	case strings.HasPrefix(u, "сек"):
		return "s"
	case strings.HasPrefix(u, "мин"):
		return "m"
	case strings.HasPrefix(u, "ч"):
		return "h"
	}
	return ""
}
