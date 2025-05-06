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

	"github.com/araddon/dateparse"
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
	// для парсинга относительного времени вида "10 сек" и т.п.
	re = regexp.MustCompile(`(\d+)\s*(секунд[ы]?|сек|с|минут[ы]?|мин|m|час[аов]?|ч|h)`)
	// для классификации категорий по корням слов
	wordRe      = regexp.MustCompile(`\p{L}+`)
	reminders   = make([]Reminder, 0)
	timers      = make(map[string]*time.Timer)
	pendingNote = make(map[int64]string) // chatID → текст напоминания, ждём время
	repeatFlag  = make(map[int64]bool)   // chatID → флаг повторений
	userCats    = make(map[int64]string) // chatID → пользовательская категория
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

	// health-check endpoint
	http.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("OK"))
	})
	go http.ListenAndServe(":8081", nil)

	// основное меню с кнопками
	menu := tgbotapi.NewReplyKeyboard(
		tgbotapi.NewKeyboardButtonRow(
			tgbotapi.NewKeyboardButton("📝 Напомни мне"),
			tgbotapi.NewKeyboardButton("📋 Список"),
		),
		tgbotapi.NewKeyboardButtonRow(
			tgbotapi.NewKeyboardButton("🔁 Повтор включён"),
			tgbotapi.NewKeyboardButton("🔁 Повтор выключен"),
		),
		tgbotapi.NewKeyboardButtonRow(
			tgbotapi.NewKeyboardButton("🏷️ Установить категорию"),
		),
	)

	u := tgbotapi.NewUpdate(0)
	u.Timeout = 60
	updates := bot.GetUpdatesChan(u)

	for upd := range updates {
		// inline-кнопки "выполнено" / "удалить"
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
			msg := tgbotapi.NewMessage(chatID,
				"👋 Привет! Напиши то, что напомнить, и время — например:\n"+
					"  • «10 мая в 14:00 сходить в аптеку»\n"+
					"  • или «через 5 мин пойти гулять»")
			msg.ReplyMarkup = menu
			bot.Send(msg)

		case "📝 напомни мне":
			bot.Send(tgbotapi.NewMessage(chatID, "✍ Напиши текст и время вместе:"))

		case "📋 список":
			showList(bot, chatID)

		case "🔁 повтор включён":
			repeatFlag[chatID] = true
			bot.Send(tgbotapi.NewMessage(chatID, "🔁 Повтор напоминаний включён"))

		case "🔁 повтор выключен":
			repeatFlag[chatID] = false
			bot.Send(tgbotapi.NewMessage(chatID, "🔁 Повтор напоминаний выключен"))

		case "🏷️ установить категорию":
			userCats[chatID] = "pending"
			bot.Send(tgbotapi.NewMessage(chatID, "🔖 Введи категорию, которую хочешь использовать всегда:"))

		case "/help":
			bot.Send(tgbotapi.NewMessage(chatID,
				"📚 Команды и подсказки:\n"+
					"  • Просто напиши «что когда», например «завтра в 9:00 завтрак»\n"+
					"  • 📝 Напомни мне — диалог по тексту+времени\n"+
					"  • 📋 Список — активные напоминания\n"+
					"  • 🔁 Повтор включён/выключен — контроль повторений\n"+
					"  • 🏷️ Установить категорию — сохраняет твою категорию"))
		default:
			// обработка установки категории
			if userCats[chatID] == "pending" {
				userCats[chatID] = upd.Message.Text
				bot.Send(tgbotapi.NewMessage(chatID,
					"✅ Категория установлена: "+upd.Message.Text))
				continue
			}
			// первое: попытка понять абсолютную дату/время
			if at, err := dateparse.ParseLocal(upd.Message.Text); err == nil && at.After(time.Now()) {
				// смогли распарсить полный текст
				// отделяем текст заметки от временной части
				note := strings.TrimSpace(
					re.ReplaceAllString(upd.Message.Text, ""),
				)
				schedule(bot, chatID, time.Until(at), note)
				continue
			}
			// второе: диалоговый режим — уже получили текст, ждём время
			if note, ok := pendingNote[chatID]; ok {
				if m := re.FindStringSubmatch(text); len(m) == 3 {
					if d, err := time.ParseDuration(m[1] + unitSuffix(m[2])); err == nil {
						delete(pendingNote, chatID)
						schedule(bot, chatID, d, note)
						continue
					}
				}
				bot.Send(tgbotapi.NewMessage(chatID,
					"⛔ Время указано неверно. Пример: 10s, 5m, 1h"))
				continue
			}
			// иначе начинаем диалог: сохраняем текст и спрашиваем время
			pendingNote[chatID] = upd.Message.Text
			bot.Send(tgbotapi.NewMessage(chatID, "⏳ Через сколько напомнить? (например: 10 сек, 5 мин, 1 час)"))
		}
	}
}

func schedule(bot *tgbotapi.BotAPI, chatID int64, d time.Duration, note string) {
	at := time.Now().Add(d)
	id := fmt.Sprintf("%d_%d", chatID, at.UnixNano())

	// категория из пользователя или автоматическая классификация
	category := classify(note)
	if cat, ok := userCats[chatID]; ok && cat != "pending" {
		category = cat
	}

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

	// основной таймер
	timer := time.AfterFunc(d, func() {
		msg := tgbotapi.NewMessage(chatID, "🔔 Напоминание: "+note)
		if repeat {
			msg.ReplyMarkup = tgbotapi.NewInlineKeyboardMarkup(
				tgbotapi.NewInlineKeyboardRow(
					tgbotapi.NewInlineKeyboardButtonData("✅ Выполнено", "done_"+id),
				),
			)
		}
		bot.Send(msg)

		// если включён повтор — переотправить через минуту, пока не нажали "Выполнено"
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
		fmt.Sprintf("✅ Запомнил! Напомню через %s (Категория: %s)", d.String(), category)))
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
	var sb strings.Builder
	sb.WriteString("📋 *Активные напоминания:*\n\n")
	for cat, items := range grouped {
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
	// отвечаем на нажатие, чтобы кнопка не висела в "часах"
	bot.Request(tgbotapi.NewCallback(cq.ID, ""))

	data := cq.Data
	chatID := cq.Message.Chat.ID

	if strings.HasPrefix(data, "done_") {
		// отметили выполненным
		id := strings.TrimPrefix(data, "done_")
		mu.Lock()
		if t, ok := timers[id]; ok {
			t.Stop()
			delete(timers, id)
		}
		removeByID(id)
		mu.Unlock()
		bot.Send(tgbotapi.NewMessage(chatID, "✅ Задача отмечена как выполненная."))
		return
	}

	// удаление напоминания из списка
	mu.Lock()
	if t, ok := timers[data]; ok {
		t.Stop()
		delete(timers, data)
	}
	removeByID(data)
	mu.Unlock()
	bot.Send(tgbotapi.NewMessage(chatID, "✅ Напоминание удалено."))
}

func removeByID(id string) {
	for i, r := range reminders {
		if r.ID == id {
			reminders = append(reminders[:i], reminders[i+1:]...)
			return
		}
	}
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

func classify(text string) string {
	switch {
	case containsRoot(text, "код", "проект", "дедлайн", "отчет"):
		return "Работа"
	case containsRoot(text, "лекц", "дз", "экзамен", "школ", "унивест"):
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
			r := strings.ToLower(root)
			if strings.HasPrefix(w, r) || strings.HasPrefix(r, w) {
				return true
			}
		}
	}
	return false
}

func unitSuffix(u string) string {
	switch {
	case strings.HasPrefix(u, "сек"):
		return "s"
	case strings.HasPrefix(u, "мин"):
		return "m"
	case strings.HasPrefix(u, "ч"):
		return "h"
	default:
		return ""
	}
}
