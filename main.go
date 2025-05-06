package main

import (
	"fmt"
	"log"
	"net/http"
	"os"
	"regexp"
	"sort"
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
	re          = regexp.MustCompile(`(\d+)\s*(секунд[ы]?|сек|с|минут[ы]?|мин|m|час[аов]?|ч|h)`)
	wordRe      = regexp.MustCompile(`\p{L}+`)
	dateTimeRe  = regexp.MustCompile(`(?i)(?:в|через)\s+(\d+:\d+|\d+\s+(?:минут[ауы]?|час[аов]?|секунд[ы]?))`)
	dayRe       = regexp.MustCompile(`(?i)(завтра|послезавтра|(\d{1,2})\s*числа)`)
	reminders   = make([]Reminder, 0)
	timers      = make(map[string]*time.Timer)
	pendingNote = make(map[int64]string)
	repeatFlag  = make(map[int64]bool)
	categoryMap = make(map[int64]string) // для временного хранения категории
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
			tgbotapi.NewKeyboardButton("➕ Добавить категорию"),
		),
		tgbotapi.NewKeyboardButtonRow(
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
		text := strings.TrimSpace(upd.Message.Text)

		switch text {
		case "/start", "привет":
			msg := tgbotapi.NewMessage(chatID, "👋 Напиши напоминание, потом укажи время (например: через 5 сек пойти гулять).")
			msg.ReplyMarkup = menu
			bot.Send(msg)

		case "📝 Напомни мне":
			bot.Send(tgbotapi.NewMessage(chatID, "✍ Что напомнить?"))

		case "➕ Добавить категорию":
			bot.Send(tgbotapi.NewMessage(chatID, "🏷️ Введите название категории для последнего напоминания:"))
			categoryMap[chatID] = "waiting"

		case "📋 Список":
			showList(bot, chatID)

		case "/help":
			bot.Send(tgbotapi.NewMessage(chatID,
				"📚 Напиши что напомнить — бот спросит через сколько.\n"+
					"📝 Напомни мне — диалог\n📋 Список — напоминания\n🔁 Повтор — включить/выключить повтор"))

		case "🔁 Повтор включен":
			repeatFlag[chatID] = true
			bot.Send(tgbotapi.NewMessage(chatID, "🔁 Повтор включён"))

		case "🔁 Повтор выключен":
			repeatFlag[chatID] = false
			bot.Send(tgbotapi.NewMessage(chatID, "🔁 Повтор выключен"))

		default:
			// Обработка команды /category
			if strings.HasPrefix(text, "/category ") {
				cat := strings.TrimSpace(strings.TrimPrefix(text, "/category "))
				linkCategory(bot, chatID, cat)
				continue
			}

			// Если ожидаем категорию
			if categoryMap[chatID] == "waiting" {
				linkCategory(bot, chatID, text)
				continue
			}

			if note, ok := pendingNote[chatID]; ok {
				at, err := parseTime(text)
				if err != nil {
					bot.Send(tgbotapi.NewMessage(chatID, "⛔ Не понял время. Пример: 'через 5 мин', 'в 17:00' или 'завтра в 10 часов'."))
					continue
				}
				delete(pendingNote, chatID)
				schedule(bot, chatID, at, note)
				continue
			}

			pendingNote[chatID] = text
			bot.Send(tgbotapi.NewMessage(chatID, "⏳ Через сколько напомнить?"))
		}
	}
}

func linkCategory(bot *tgbotapi.BotAPI, chatID int64, cat string) {
	mu.Lock()
	defer mu.Unlock()

	for i := len(reminders) - 1; i >= 0; i-- {
		r := &reminders[i]
		if r.ChatID == chatID {
			r.Category = cat
			bot.Send(tgbotapi.NewMessage(chatID, fmt.Sprintf("🏷️ Категория '%s' привязана.", cat)))
			delete(categoryMap, chatID)
			return
		}
	}
	bot.Send(tgbotapi.NewMessage(chatID, "❌ Нет напоминаний для привязки категории."))
	delete(categoryMap, chatID)
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

func schedule(bot *tgbotapi.BotAPI, chatID int64, at time.Time, note string) {
	now := time.Now()
	if at.Before(now) {
		bot.Send(tgbotapi.NewMessage(chatID, "⚠️ Время уже прошло."))
		return
	}

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

	duration := at.Sub(now)
	timer := time.AfterFunc(duration, func() {
		msg := tgbotapi.NewMessage(chatID, "🔔 Напоминание: "+note)
		msg.ReplyMarkup = tgbotapi.NewInlineKeyboardMarkup(
			tgbotapi.NewInlineKeyboardRow(
				tgbotapi.NewInlineKeyboardButtonData("✅ Выполнено", id),
			),
		)
		bot.Send(msg)

		if repeat {
			timers[id] = time.AfterFunc(1*time.Minute, func() {
				if stillExists(id) {
					bot.Send(tgbotapi.NewMessage(chatID, "🔁 Повтор: "+note))
				}
			})
		}
	})
	timers[id] = timer

	bot.Send(tgbotapi.NewMessage(chatID,
		fmt.Sprintf("✅ Запомнил! Напомню %s (Категория: %s)", at.Format("02.01 15:04"), category)))
}

func parseTime(input string) (time.Time, error) {
	input = strings.ToLower(input)

	now := time.Now().Truncate(time.Second)

	// Проверяем формат "через X [минут/часов]"
	if m := re.FindStringSubmatch(input); len(m) == 3 {
		num, _ := strconv.Atoi(m[1])
		unit := m[2]
		var d time.Duration
		switch {
		case strings.HasPrefix(unit, "сек"):
			d = time.Duration(num) * time.Second
		case strings.HasPrefix(unit, "мин"):
			d = time.Duration(num) * time.Minute
		case strings.HasPrefix(unit, "ч"):
			d = time.Duration(num) * time.Hour
		}
		if d > 0 {
			return now.Add(d), nil
		}
	}

	// Проверяем формат "в 17:00"
	if m := dateTimeRe.FindStringSubmatch(input); len(m) == 2 {
		timeStr := m[1]
		if strings.Contains(timeStr, ":") {
			hourMin := strings.Split(timeStr, ":")
			h, _ := strconv.Atoi(hourMin[0])
			m, _ := strconv.Atoi(hourMin[1])
			today := time.Date(now.Year(), now.Month(), now.Day(), h, m, 0, 0, now.Location())
			if today.After(now) {
				return today, nil
			}
			return today.AddDate(0, 0, 1), nil
		}

		// Например: "в 10 часов"
		if parts := strings.Fields(timeStr); len(parts) >= 1 {
			h, _ := strconv.Atoi(parts[0])
			today := time.Date(now.Year(), now.Month(), now.Day(), h, 0, 0, 0, now.Location())
			if today.After(now) {
				return today, nil
			}
			return today.AddDate(0, 0, 1), nil
		}
	}

	// "завтра в 10:00"
	if m := dayRe.FindStringSubmatch(input); len(m) >= 2 {
		day := m[1]
		var when time.Time
		switch day {
		case "завтра":
			when = now.AddDate(0, 0, 1)
		case "послезавтра":
			when = now.AddDate(0, 0, 2)
		default:
			if mday := m[2]; mday != "" {
				d, _ := strconv.Atoi(mday)
				when = time.Date(now.Year(), now.Month(), d, 0, 0, 0, 0, now.Location())
				if when.Before(now) {
					nextMonth := now.Month() + 1
					year := now.Year()
					if nextMonth > 12 {
						nextMonth = 1
						year++
					}
					when = time.Date(year, nextMonth, d, 0, 0, 0, 0, now.Location())
				}
			}
		}

		if when.IsZero() {
			return time.Time{}, fmt.Errorf("не удалось распознать дату")
		}

		// Теперь проверим время после дня
		if tm := dateTimeRe.FindStringSubmatch(input); len(tm) >= 2 && strings.Contains(tm[1], ":") {
			hm := strings.Split(tm[1], ":")
			h, _ := strconv.Atoi(hm[0])
			m, _ := strconv.Atoi(hm[1])
			when = time.Date(when.Year(), when.Month(), when.Day(), h, m, 0, 0, when.Location())
		} else {
			when = when.Add(9 * time.Hour) // если просто день указан — по умолчанию в 9 утра
		}

		return when, nil
	}

	return time.Time{}, fmt.Errorf("не распознал формат времени")
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
	var keys []string
	for k := range grouped {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	var text strings.Builder
	for _, cat := range keys {
		items := grouped[cat]
		text.WriteString(fmt.Sprintf("🔖 *%s*:\n", cat))
		for _, r := range items {
			text.WriteString(fmt.Sprintf("• %s (%s)\n", r.Note, r.At.Format("02.01 15:04")))
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
