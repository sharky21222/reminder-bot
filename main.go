package main

import (
	"fmt"
	"log"
	"net/http"
	"os"
	"regexp"
	"strconv"
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
	// для «через 5 сек», «10m», «1h»…
	reRel = regexp.MustCompile(`(?i)(?:через\s*)?(\d+)\s*(секунд[ы]?|сек|с|минут[ы]?|мин|m|час[аов]?|ч|h)`)
	// для «10 мая в 14:00 заметка»
	reAbs = regexp.MustCompile(`(?i)^(?:напомни(?:\s+мне)?\s*)?(\d{1,2})\s*(января|февраля|марта|апреля|мая|июня|июля|августа|сентября|октября|ноября|декабря)\s*(?:в\s*)?(\d{1,2}):(\d{2})\s+(.+)$`)
	// для «завтра в 5:30 сделать…»
	reTomorrow = regexp.MustCompile(`(?i)^(?:напомни(?:\s+мне)?\s*)?завтра(?:\s*в\s*(\d{1,2})(?::|\.)(\d{2}))?\s+(.+)$`)

	monthMap = map[string]time.Month{
		"января":   time.January,
		"февраля":  time.February,
		"марта":    time.March,
		"апреля":   time.April,
		"мая":      time.May,
		"июня":     time.June,
		"июля":     time.July,
		"августа":  time.August,
		"сентября": time.September,
		"октября":  time.October,
		"ноября":   time.November,
		"декабря":  time.December,
	}

	// состояние чата
	repeatFlag  = make(map[int64]bool)   // повторять или нет
	userCats    = make(map[int64]string) // пользовательская категория
	pendingNote = make(map[int64]string) // ждет уточнения времени

	// собственно напоминания
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

	// health-check
	http.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("OK"))
	})
	go http.ListenAndServe(":8081", nil)

	// главное меню
	menu := tgbotapi.NewReplyKeyboard(
		tgbotapi.NewKeyboardButtonRow(
			tgbotapi.NewKeyboardButton("📝 Напомни мне"),
			tgbotapi.NewKeyboardButton("📋 Список"),
		),
		tgbotapi.NewKeyboardButtonRow(
			tgbotapi.NewKeyboardButton("🔁 Повтор вкл"),
			tgbotapi.NewKeyboardButton("🔁 Повтор выкл"),
		),
		tgbotapi.NewKeyboardButtonRow(
			tgbotapi.NewKeyboardButton("🏷️ Установить категорию"),
		),
	)

	u := tgbotapi.NewUpdate(0)
	u.Timeout = 60
	updates := bot.GetUpdatesChan(u)

	for upd := range updates {
		// inline-кнопки
		if upd.CallbackQuery != nil {
			handleCallback(bot, upd.CallbackQuery)
			continue
		}
		if upd.Message == nil {
			continue
		}

		chatID := upd.Message.Chat.ID
		text := strings.TrimSpace(upd.Message.Text)

		switch strings.ToLower(text) {
		case "/start", "привет":
			msg := tgbotapi.NewMessage(chatID,
				"👋 Привет! Напиши «что когда», например:\n"+
					" • «через 5 мин кофеить»\n"+
					" • «10 мая в 14:00 сходить в аптеку»")
			msg.ReplyMarkup = menu
			bot.Send(msg)

		case "📝 напомни мне":
			bot.Send(tgbotapi.NewMessage(chatID, "✍ Напиши заметку и время вместе, например «через 3 мин прочитать статью»"))

		case "📋 список":
			showList(bot, chatID)

		case "🔁 повтор вкл":
			repeatFlag[chatID] = true
			bot.Send(tgbotapi.NewMessage(chatID, "🔁 Повтор включён"))

		case "🔁 повтор выкл":
			repeatFlag[chatID] = false
			bot.Send(tgbotapi.NewMessage(chatID, "🔁 Повтор выключен"))

		case "🏷️ установить категорию":
			userCats[chatID] = "pending"
			bot.Send(tgbotapi.NewMessage(chatID, "🔖 Введи название своей категории:"))

		case "/help":
			bot.Send(tgbotapi.NewMessage(chatID,
				"📚 Инструкция:\n"+
					" • Просто напиши «что когда» в одном сообщении\n"+
					" • 📝 Напомни мне — начать диалог\n"+
					" • 📋 Список — показать напоминания\n"+
					" • 🔁 Повтор вкл/выкл — переключить автоповтор\n"+
					" • 🏷️ Категория — задать/изменить свою категорию"))

		default:
			// если ждем ввода категории
			if userCats[chatID] == "pending" {
				userCats[chatID] = text
				bot.Send(tgbotapi.NewMessage(chatID, "✅ Категория установлена: "+text))
				continue
			}
			// полный парсинг одного сообщения
			if at, note, ok := parseInput(text); ok {
				schedule(bot, chatID, time.Until(at), note)
				continue
			}
			// уточняем время после заметки
			if note, ok := pendingNote[chatID]; ok {
				if m := reRel.FindStringSubmatch(text); len(m) == 3 {
					if d, err := time.ParseDuration(m[1] + unitSuffix(m[2])); err == nil {
						delete(pendingNote, chatID)
						schedule(bot, chatID, d, note)
						continue
					}
				}
				bot.Send(tgbotapi.NewMessage(chatID, "⛔ Неверный формат времени. Пример: 10s, 5m, 1h"))
				continue
			}
			// начинаем диалог: сохраним заметку и спросим время
			pendingNote[chatID] = text
			bot.Send(tgbotapi.NewMessage(chatID, "⏳ Через сколько напомнить?"))
		}
	}
}

// parseInput пытается распарсить текст сразу в дату+заметку
func parseInput(text string) (time.Time, string, bool) {
	now := time.Now()
	// 1) абсолютная дата
	if m := reAbs.FindStringSubmatch(text); len(m) == 6 {
		day := toInt(m[1])
		mon := monthMap[strings.ToLower(m[2])]
		hour := toInt(m[3])
		min := toInt(m[4])
		note := m[5]
		at := time.Date(now.Year(), mon, day, hour, min, 0, 0, now.Location())
		if at.Before(now) {
			at = at.AddDate(1, 0, 0)
		}
		return at, note, true
	}
	// 2) завтра
	if m := reTomorrow.FindStringSubmatch(text); len(m) == 4 {
		h := 9
		mi := 0
		if m[1] != "" {
			h = toInt(m[1])
			if m[2] != "" {
				mi = toInt(m[2])
			}
		}
		note := m[3]
		at := time.Date(now.Year(), now.Month(), now.Day()+1, h, mi, 0, 0, now.Location())
		return at, note, true
	}
	// 3) через
	if m := reRel.FindStringSubmatch(text); len(m) == 3 {
		if d, err := time.ParseDuration(m[1] + unitSuffix(m[2])); err == nil {
			note := reRel.ReplaceAllString(text, "")
			return now.Add(d), strings.TrimSpace(note), true
		}
	}
	return time.Time{}, "", false
}

func schedule(bot *tgbotapi.BotAPI, chatID int64, d time.Duration, note string) {
	at := time.Now().Add(d)
	id := fmt.Sprintf("%d_%d", chatID, at.UnixNano())
	cat := classify(note)
	if c := userCats[chatID]; c != "" {
		cat = c
	}
	rep := repeatFlag[chatID]

	mu.Lock()
	reminders = append(reminders, Reminder{ID: id, ChatID: chatID, Note: note, At: at, Category: cat, NeedRepeat: rep})
	mu.Unlock()

	// отправляем уведомление
	time.AfterFunc(d, func() {
		msg := tgbotapi.NewMessage(chatID, "🔔 Напоминание: "+note)
		if rep {
			msg.ReplyMarkup = tgbotapi.NewInlineKeyboardMarkup(
				tgbotapi.NewInlineKeyboardRow(
					tgbotapi.NewInlineKeyboardButtonData("✅ Выполнено", "done_"+id),
				),
			)
		}
		bot.Send(msg)

		if rep {
			// через минуту напомнить снова, если не удалено
			timers[id] = time.AfterFunc(1*time.Minute, func() {
				if stillExists(id) {
					bot.Send(tgbotapi.NewMessage(chatID, "🔁 Повтор: "+note))
				}
			})
		}
	})

	bot.Send(tgbotapi.NewMessage(chatID, "✅ Запомню!"))
}

func showList(bot *tgbotapi.BotAPI, chatID int64) {
	mu.Lock()
	defer mu.Unlock()
	g := map[string][]Reminder{}
	for _, r := range reminders {
		if r.ChatID == chatID {
			g[r.Category] = append(g[r.Category], r)
		}
	}
	if len(g) == 0 {
		bot.Send(tgbotapi.NewMessage(chatID, "📋 Нет напоминаний"))
		return
	}

	var rows [][]tgbotapi.InlineKeyboardButton
	var sb strings.Builder
	sb.WriteString("📋 *Список*:\n\n")
	for cat, items := range g {
		sb.WriteString(fmt.Sprintf("🔖 *%s*:\n", cat))
		for _, r := range items {
			rem := time.Until(r.At).Truncate(time.Second)
			sb.WriteString(fmt.Sprintf("• %s (через %s)\n", r.Note, rem))
			rows = append(rows, tgbotapi.NewInlineKeyboardRow(
				tgbotapi.NewInlineKeyboardButtonData("❌ Удалить", r.ID),
			))
		}
		sb.WriteString("\n")
	}

	msg := tgbotapi.NewMessage(chatID, sb.String())
	msg.ParseMode = "Markdown"
	msg.ReplyMarkup = tgbotapi.NewInlineKeyboardMarkup(rows...)
	bot.Send(msg)
}

func handleCallback(bot *tgbotapi.BotAPI, cq *tgbotapi.CallbackQuery) {
	// подтвердить Telegram
	bot.Request(tgbotapi.NewCallback(cq.ID, ""))

	data := cq.Data
	chatID := cq.Message.Chat.ID

	if strings.HasPrefix(data, "done_") {
		id := strings.TrimPrefix(data, "done_")
		mu.Lock()
		removeTimerAndReminder(id)
		mu.Unlock()
		bot.Send(tgbotapi.NewMessage(chatID, "✅ Выполнено"))
		return
	}

	mu.Lock()
	removeTimerAndReminder(data)
	mu.Unlock()
	bot.Send(tgbotapi.NewMessage(chatID, "✅ Удалено"))
}

func removeTimerAndReminder(id string) {
	if t, ok := timers[id]; ok {
		t.Stop()
		delete(timers, id)
	}
	for i, r := range reminders {
		if r.ID == id {
			reminders = append(reminders[:i], reminders[i+1:]...)
			return
		}
	}
}

func stillExists(id string) bool {
	for _, r := range reminders {
		if r.ID == id {
			return true
		}
	}
	return false
}

func classify(text string) string {
	l := strings.ToLower(text)
	switch {
	case strings.Contains(l, "код"), strings.Contains(l, "проект"), strings.Contains(l, "дедлайн"):
		return "Работа"
	case strings.Contains(l, "лекц"), strings.Contains(l, "экзам"), strings.Contains(l, "школ"):
		return "Учёба"
	case strings.Contains(l, "врач"), strings.Contains(l, "здоров"), strings.Contains(l, "лекарств"):
		return "Здоровье"
	default:
		return "Другое"
	}
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

func toInt(s string) int {
	n, _ := strconv.Atoi(s)
	return n
}
