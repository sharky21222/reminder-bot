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
	ID       string
	ChatID   int64
	Note     string
	At       time.Time
	Category string
}

var (
	re        = regexp.MustCompile(`(\d+)\s*(секунд[ы]?|сек|с|минут[ы]?|мин|m|час[аов]?|ч|h)\s*(.*)`)
	reminders = make([]Reminder, 0)
	mu        sync.Mutex
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
		text := strings.TrimSpace(strings.ToLower(upd.Message.Text))

		if text == "/start" || strings.Contains(text, "привет") {
			msg := tgbotapi.NewMessage(upd.Message.Chat.ID, "👋 Привет! Я бот‑напоминалка.")
			msg.ReplyMarkup = menu
			bot.Send(msg)
			continue
		}

		switch {
		case text == "📝 напомни мне":
			msg := tgbotapi.NewMessage(upd.Message.Chat.ID,
				"✍ Введите, например:\nнапомни через 5 сек пойти гулять")
			msg.ReplyMarkup = menu
			bot.Send(msg)

		case text == "📋 список":
			showList(bot, upd.Message.Chat.ID)

		case text == "/help":
			bot.Send(tgbotapi.NewMessage(upd.Message.Chat.ID,
				"📚 Команды:\n"+
					"/remind <время> <текст>\n"+
					"Например: напомни через 5 сек пойти гулять\n"+
					"📝 Напомни мне — подсказка\n"+
					"📋 Список — активные напоминания"))

		default:
			if dur, note, ok := parseAny(text); ok {
				schedule(bot, upd.Message.Chat.ID, dur, note)
			}
		}
	}
}

func schedule(bot *tgbotapi.BotAPI, chatID int64, d time.Duration, note string) {
	at := time.Now().Add(d)
	id := fmt.Sprintf("%d_%d", chatID, at.UnixNano())
	category := classify(note)

	mu.Lock()
	reminders = append(reminders, Reminder{ID: id, ChatID: chatID, Note: note, At: at, Category: category})
	mu.Unlock()

	bot.Send(tgbotapi.NewMessage(chatID, fmt.Sprintf(
		"⏳ Ок, напомню через %s\nКатегория: %s", d.String(), category)))
	go func() {
		time.Sleep(d)
		bot.Send(tgbotapi.NewMessage(chatID, "🔔 Напоминание: "+note))
		removeByID(id)
	}()
}

func showList(bot *tgbotapi.BotAPI, chatID int64) {
	mu.Lock()
	defer mu.Unlock()

	// сгруппировать по категории
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

	// простой вывод: категории в алфавитном порядке
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
			rem := fmt.Sprintf("• %s (через %s)", r.Note, time.Until(r.At).Truncate(time.Second))
			lines = append(lines, rem)
			rows = append(rows, tgbotapi.NewInlineKeyboardRow(
				tgbotapi.NewInlineKeyboardButtonData("❌ Удалить", r.ID),
			))
		}
		lines = append(lines, "") // пустая строка между группами
	}

	msg := tgbotapi.NewMessage(chatID, strings.Join(lines, "\n"))
	msg.ParseMode = "Markdown"
	msg.ReplyMarkup = tgbotapi.NewInlineKeyboardMarkup(rows...)
	bot.Send(msg)
}

func handleCallback(bot *tgbotapi.BotAPI, cq *tgbotapi.CallbackQuery) {
	removeByID(cq.Data)
	callback := tgbotapi.NewCallback(cq.ID, "Удалено")
	bot.Request(callback)
	bot.Send(tgbotapi.NewMessage(cq.Message.Chat.ID, "✅ Напоминание удалено"))
}

func removeByID(id string) {
	mu.Lock()
	defer mu.Unlock()
	for i, r := range reminders {
		if r.ID == id {
			reminders = append(reminders[:i], reminders[i+1:]...)
			return
		}
	}
}

// classify присваивает заметке тему по ключевым словам
func classify(text string) string {
	switch {
	case containsAny(text, "код", "проект", "собеседование", "отчет"):
		return "Работа"
	case containsAny(text, "учеба", "лекция", "дз", "экзамен", "учить"):
		return "Учёба"
	case containsAny(text, "прогулка", "спорт", "здоровье", "медицина", "аптека"):
		return "Здоровье"
	default:
		return "Другое"
	}
}

func containsAny(s string, keywords ...string) bool {
	for _, k := range keywords {
		if strings.Contains(s, k) {
			return true
		}
	}
	return false
}

func parseAny(text string) (time.Duration, string, bool) {
	text = strings.TrimPrefix(text, "/remind ")
	text = strings.TrimPrefix(text, "напомни ")
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
