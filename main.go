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
	wordRe    = regexp.MustCompile(`\p{L}+`)
	reminders = make([]Reminder, 0)
	timers    = make(map[string]*time.Timer)
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

	// сохраняем напоминание
	mu.Lock()
	reminders = append(reminders, Reminder{ID: id, ChatID: chatID, Note: note, At: at, Category: category})
	// создаём таймер и сохраняем его
	timers[id] = time.AfterFunc(d, func() {
		bot.Send(tgbotapi.NewMessage(chatID, "🔔 Напоминание: "+note))
		removeByID(id)
	})
	mu.Unlock()

	bot.Send(tgbotapi.NewMessage(chatID, fmt.Sprintf(
		"⏳ Ок, напомню через %s\nКатегория: %s", d.String(), category)))
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
	id := cq.Data

	// остановить таймер и удалить напоминание
	mu.Lock()
	if t, ok := timers[id]; ok {
		t.Stop()
		delete(timers, id)
	}
	removeByID(id)
	mu.Unlock()

	// ответить на кнопку
	callback := tgbotapi.NewCallback(cq.ID, "Удалено")
	bot.Request(callback)
	bot.Send(tgbotapi.NewMessage(cq.Message.Chat.ID, "✅ Напоминание удалено"))
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
	case containsRoot(text, "уборк", "стирк", "готовк", "помыв", "ремонт", "куп", "посуд", "мусор", "прачк", "сад"):
		return "Дом"
	case containsRoot(text, "куп", "заказ", "пополн", "бюджет", "счет", "оплат", "платеж", "налог", "банк", "карт", "расход"):
		return "Покупки/Финансы"
	case containsRoot(text, "кин", "сериал", "игр", "музык", "книж", "встреч", "вечеринк", "отдых", "путешеств", "хобби", "концерт"):
		return "Развлечения"
	default:
		return "Другое"
	}
}

func containsRoot(text string, roots ...string) bool {
	words := wordRe.FindAllString(strings.ToLower(text), -1)
	for _, w := range words {
		for _, root := range roots {
			r := strings.ToLower(root)
			if strings.HasPrefix(w, r) || strings.HasPrefix(r, w) {
				return true
			}
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
