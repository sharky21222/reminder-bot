package main

import (
	"fmt"
	"log"
	"net/http"
	"os"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
)

type Reminder struct {
	ID        string
	ChatID    int64
	Note      string
	At        time.Time
	Category  string
	Confirmed bool
}

var (
	re          = regexp.MustCompile(`(\d+)\s*(секунд[ы]?|сек|с|минут[ы]?|мин|m|час[аов]?|ч|h)`)
	wordRe      = regexp.MustCompile(`\p{L}+`)
	reminders   = make([]Reminder, 0)
	timers      = make(map[string]*time.Timer)
	pendingNote = make(map[int64]string) // chatID → note, ожидает время
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

		// если ждём от этого чата время
		if note, ok := pendingNote[chatID]; ok {
			if m := re.FindStringSubmatch(text); len(m) == 3 {
				d, err := time.ParseDuration(m[1] + unitSuffix(m[2]))
				if err == nil {
					delete(pendingNote, chatID)
					schedule(bot, chatID, d, note)
					continue
				}
			}
			bot.Send(tgbotapi.NewMessage(chatID, "⛔ Неверный формат времени. Примеры: 10s, 5m, 1h"))
			continue
		}

		switch {
		case text == "/start" || strings.Contains(text, "привет"):
			msg := tgbotapi.NewMessage(chatID, "👋 Привет! Я бот‑напоминалка.")
			msg.ReplyMarkup = menu
			bot.Send(msg)

		case text == "📝 напомни мне":
			bot.Send(tgbotapi.NewMessage(chatID, "✍ Что напомнить?"))

		case text == "📋 список":
			showList(bot, chatID)

		case text == "/help":
			bot.Send(tgbotapi.NewMessage(chatID,
				"📚 Напишите то, что хотите запомнить — бот спросит “Через сколько?”\n"+
					"Или команды:\n"+
					"📝 Напомни мне — начать диалог\n"+
					"📋 Список — активные напоминания\n"+
					"✅ После напоминания нажмите 'Выполнено', иначе через 2 минуты я повторю."))

		default:
			pendingNote[chatID] = upd.Message.Text
			bot.Send(tgbotapi.NewMessage(chatID, "⏳ Через сколько напомнить? (например: 10s, 5m, 1h)"))
		}
	}
}

func schedule(bot *tgbotapi.BotAPI, chatID int64, d time.Duration, note string) {
	at := time.Now().Add(d)
	id := fmt.Sprintf("%d_%d", chatID, at.UnixNano())
	category := classify(note)

	rem := Reminder{ID: id, ChatID: chatID, Note: note, At: at, Category: category}
	mu.Lock()
	reminders = append(reminders, rem)
	mu.Unlock()

	timers[id] = time.AfterFunc(d, func() {
		btn := tgbotapi.NewInlineKeyboardMarkup(
			tgbotapi.NewInlineKeyboardRow(tgbotapi.NewInlineKeyboardButtonData("✅ Выполнено", "done_"+id)),
		)
		msg := tgbotapi.NewMessage(chatID, "🔔 Напоминание: "+note)
		msg.ReplyMarkup = btn
		bot.Send(msg)

		time.AfterFunc(2*time.Minute, func() {
			mu.Lock()
			for _, r := range reminders {
				if r.ID == id && !r.Confirmed {
					bot.Send(tgbotapi.NewMessage(chatID, "🔁 Повторное напоминание: "+note))
					break
				}
			}
			mu.Unlock()
		})
	})

	bot.Send(tgbotapi.NewMessage(chatID,
		fmt.Sprintf("✅ Запомнил! Напомню через %s (Категория: %s)", d.String(), category)))
}

func showList(bot *tgbotapi.BotAPI, chatID int64) {
	mu.Lock()
	defer mu.Unlock()

	groups := map[string][]Reminder{}
	for _, r := range reminders {
		if r.ChatID == chatID {
			groups[r.Category] = append(groups[r.Category], r)
		}
	}
	if len(groups) == 0 {
		bot.Send(tgbotapi.NewMessage(chatID, "📋 Нет активных напоминаний"))
		return
	}

	cats := make([]string, 0, len(groups))
	for c := range groups {
		cats = append(cats, c)
	}
	sort.Strings(cats)

	var lines []string
	var rows [][]tgbotapi.InlineKeyboardButton
	for _, cat := range cats {
		lines = append(lines, fmt.Sprintf("🔖 *%s*:", cat))
		for _, r := range groups[cat] {
			remaining := time.Until(r.At).Truncate(time.Second)
			lines = append(lines, fmt.Sprintf("• %s (через %s)", r.Note, remaining))
			rows = append(rows, tgbotapi.NewInlineKeyboardRow(
				tgbotapi.NewInlineKeyboardButtonData("❌ Удалить", r.ID),
			))
		}
		lines = append(lines, "")
	}

	msg := tgbotapi.NewMessage(chatID, strings.Join(lines, "\n"))
	msg.ParseMode = "Markdown"
	msg.ReplyMarkup = tgbotapi.NewInlineKeyboardMarkup(rows...)
	bot.Send(msg)
}

func handleCallback(bot *tgbotapi.BotAPI, cq *tgbotapi.CallbackQuery) {
	data := cq.Data
	if strings.HasPrefix(data, "done_") {
		id := strings.TrimPrefix(data, "done_")
		mu.Lock()
		for i, r := range reminders {
			if r.ID == id {
				reminders[i].Confirmed = true
				break
			}
		}
		mu.Unlock()
		bot.Send(tgbotapi.NewMessage(cq.Message.Chat.ID, "✅ Спасибо, отмечено как выполнено"))
	} else {
		id := data
		mu.Lock()
		if t, ok := timers[id]; ok {
			t.Stop()
			delete(timers, id)
		}
		removeByID(id)
		mu.Unlock()
		bot.Send(tgbotapi.NewMessage(cq.Message.Chat.ID, "✅ Напоминание удалено"))
	}
	bot.Request(tgbotapi.NewCallback(cq.ID, "Готово"))
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
	case containsRoot(text, "код", "проект", "встреч", "митинг", "дедлайн", "отчет", "презентац", "доклад", "задач", "собеседован"):
		return "Работа"
	case containsRoot(text, "лекц", "семинар", "дз", "экзамен", "тест", "реферат", "курс", "университет", "колледж", "школ", "уч"):
		return "Учёба"
	case containsRoot(text, "спор", "тренир", "прогул", "здоров", "медицин", "аптек", "лекарств", "диет", "врач", "анализ", "йог", "медит"):
		return "Здоровье"
	case containsRoot(text, "уборк", "стирк", "готовк", "помыв", "ремонт", "посуд", "мусор", "прачк", "сад"):
		return "Дом"
	case containsRoot(text, "куп", "заказ", "пополн", "бюджет", "счет", "оплат", "платеж", "налог", "банк", "карт", "расход"):
		return "Финансы"
	case containsRoot(text, "кин", "сериал", "игр", "музык", "книж", "вечеринк", "отдых", "путешеств", "хобби", "концерт"):
		return "Развлечения"
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

func parseAny(text string) (time.Duration, string, bool) {
	return 0, "", false
}
