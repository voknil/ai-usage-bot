// Package bot ties the sources, the renderer and Telegram together: it keeps
// one status message up to date, sends threshold alerts and answers the
// refresh button.
package bot

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"

	"github.com/voknil/ai-usage-bot/internal/alerts"
	"github.com/voknil/ai-usage-bot/internal/render"
	"github.com/voknil/ai-usage-bot/internal/state"
	"github.com/voknil/ai-usage-bot/internal/telegram"
	"github.com/voknil/ai-usage-bot/internal/usage"
)

// Callback data. The legacy value keeps buttons on messages created by the
// shell version working.
const (
	callbackRefresh       = "refresh"
	callbackRefreshLegacy = "refresh_claude_usage"

	buttonIdle    = "↻ Обновить сейчас ✅"
	buttonBusy    = "⏳ Обновляю..."
	buttonFailed  = "⚠️ Не вышло — повторить"
	pollTimeout   = 50
	pollRetryWait = 5 * time.Second
)

// Sources are the data providers. Any may be nil.
type Sources struct {
	Claude   func(ctx context.Context) (*usage.Claude, error)
	OpenAI   func(ctx context.Context) (*usage.OpenAI, error)
	Services []ServiceSource
}

// ServiceSource is one public status page.
type ServiceSource struct {
	Name  string
	Fetch func(ctx context.Context) (*usage.Service, error)
}

// Options configures a Bot.
type Options struct {
	ChatID          int64 // pinned chat, 0 = first /start wins
	RefreshInterval time.Duration
	CacheTTL        time.Duration
	Thresholds      []float64
	MessagePath     string
	AlertPath       string
	OffsetPath      string
	Location        *time.Location
	Now             func() time.Time
}

type message struct {
	ChatID    int64 `json:"chat_id"`
	MessageID int64 `json:"message_id"`
}

type cached[T any] struct {
	val *T
	at  time.Time
}

// Bot is the running service.
type Bot struct {
	tg  *telegram.Client
	src Sources
	opt Options
	log *slog.Logger

	mu       sync.Mutex // serialises refreshes and guards everything below
	claude   cached[usage.Claude]
	openai   cached[usage.OpenAI]
	services []cached[usage.Service]
	msg      message
	alerts   alerts.Store
	lastText string
}

// New loads persisted state and returns a Bot.
func New(tg *telegram.Client, src Sources, opt Options, log *slog.Logger) (*Bot, error) {
	if opt.Now == nil {
		opt.Now = time.Now
	}
	if opt.Location == nil {
		opt.Location = time.Local
	}
	b := &Bot{tg: tg, src: src, opt: opt, log: log, services: make([]cached[usage.Service], len(src.Services))}
	if err := state.LoadJSON(opt.MessagePath, &b.msg); err != nil {
		return nil, err
	}
	if err := state.LoadJSON(opt.AlertPath, &b.alerts); err != nil {
		return nil, err
	}
	if opt.ChatID != 0 && b.msg.ChatID != opt.ChatID {
		b.msg = message{ChatID: opt.ChatID}
	}
	return b, nil
}

// Run refreshes on a schedule and serves button presses until ctx ends.
func (b *Bot) Run(ctx context.Context) error {
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		b.schedule(ctx)
	}()
	err := b.poll(ctx)
	wg.Wait()
	if errors.Is(err, context.Canceled) {
		return nil
	}
	return err
}

func (b *Bot) schedule(ctx context.Context) {
	t := time.NewTicker(b.opt.RefreshInterval)
	defer t.Stop()
	for {
		if err := b.Refresh(ctx, false); err != nil && ctx.Err() == nil {
			b.log.Error("refresh failed", "err", err)
		}
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}

// Refresh fetches stale sources (all of them when force is set), sends or
// retracts alerts and updates the status message.
func (b *Bot) Refresh(ctx context.Context, force bool) error {
	b.mu.Lock()
	defer b.mu.Unlock()

	b.fetchAll(ctx, force)

	if b.msg.ChatID == 0 {
		b.log.Info("no chat yet: send /start to the bot")
		return nil
	}
	if err := b.processAlerts(ctx); err != nil {
		b.log.Warn("alerts", "err", err)
	}
	return b.publish(ctx, false)
}

func (b *Bot) fresh(at time.Time, force bool) bool {
	return !force && !at.IsZero() && b.opt.Now().Sub(at) < b.opt.CacheTTL
}

// fetchAll refreshes each stale source. A failing source keeps its previous
// value, so a hiccup shows up as "stale" rather than an empty message.
func (b *Bot) fetchAll(ctx context.Context, force bool) {
	if b.src.Claude != nil && !b.fresh(b.claude.at, force) {
		if v, err := b.src.Claude(ctx); err != nil {
			b.log.Warn("claude usage", "err", err)
		} else {
			b.claude = cached[usage.Claude]{v, b.opt.Now()}
		}
	}
	if b.src.OpenAI != nil && !b.fresh(b.openai.at, force) {
		if v, err := b.src.OpenAI(ctx); err != nil {
			b.log.Warn("openai usage", "err", err)
		} else {
			b.openai = cached[usage.OpenAI]{v, b.opt.Now()}
		}
	}
	for i, s := range b.src.Services {
		if b.fresh(b.services[i].at, force) {
			continue
		}
		if v, err := s.Fetch(ctx); err != nil {
			b.log.Warn("service status", "service", s.Name, "err", err)
		} else {
			b.services[i] = cached[usage.Service]{v, b.opt.Now()}
		}
	}
}

func (b *Bot) snapshot() render.Snapshot {
	s := render.Snapshot{Claude: b.claude.val, OpenAI: b.openai.val}
	for i, src := range b.src.Services {
		s.Services = append(s.Services, render.ServiceView{Name: src.Name, Svc: b.services[i].val})
	}
	return s
}

func (b *Bot) text() string {
	return render.Status(b.snapshot(), b.opt.Now(), b.opt.Location)
}

func (b *Bot) targets() []alerts.Target {
	var out []alerts.Target
	add := func(key, label string, w *usage.Window) {
		if w != nil {
			out = append(out, alerts.Target{Key: key, Label: label, Percent: w.Percent, ResetsAt: w.ResetsAt})
		}
	}
	if c := b.claude.val; c != nil {
		add("claude_5h", "Claude 5h", c.Session)
		add("claude_week", "Claude week", c.Week)
	}
	if o := b.openai.val; o != nil && o.Codex != nil {
		add("codex_5h", "Codex 5h", o.Codex.Session)
		add("codex_week", "Codex week", o.Codex.Week)
	}
	return out
}

func (b *Bot) processAlerts(ctx context.Context) error {
	plans := b.alerts.Evaluate(b.targets(), b.opt.Thresholds)
	var errs []error
	for _, p := range plans {
		for _, id := range p.Delete {
			if err := b.tg.DeleteMessage(ctx, b.msg.ChatID, id); err != nil && !telegram.IsGone(err) {
				errs = append(errs, err)
			}
		}
		for _, n := range p.Notify {
			id, err := b.tg.SendMessage(ctx, b.msg.ChatID, render.Alert(n, b.opt.Location), "", false)
			if err != nil {
				errs = append(errs, err)
				continue
			}
			b.alerts.Sent(n.Key, n.Threshold, id)
		}
	}
	if err := state.SaveJSON(b.opt.AlertPath, &b.alerts); err != nil {
		errs = append(errs, err)
	}
	return errors.Join(errs...)
}

func keyboard(label string) string {
	return telegram.Keyboard(telegram.Button{Text: label, CallbackData: callbackRefresh})
}

// publish updates the status message, creating it when missing. With recreate
// set, any existing message is replaced.
func (b *Bot) publish(ctx context.Context, recreate bool) error {
	text := b.text()
	if recreate && b.msg.MessageID != 0 {
		if err := b.tg.DeleteMessage(ctx, b.msg.ChatID, b.msg.MessageID); err != nil && !telegram.IsGone(err) {
			b.log.Warn("delete old status message", "err", err)
		}
		b.msg.MessageID = 0
	}
	if b.msg.MessageID == 0 {
		return b.create(ctx, text)
	}
	if text == b.lastText {
		return nil
	}
	err := b.tg.EditMessage(ctx, b.msg.ChatID, b.msg.MessageID, text, keyboard(buttonIdle))
	switch {
	case err == nil, telegram.IsNotModified(err):
		b.lastText = text
		return nil
	case telegram.IsGone(err):
		b.log.Info("status message is gone, creating a new one")
		return b.create(ctx, text)
	default:
		return fmt.Errorf("edit status message: %w", err)
	}
}

func (b *Bot) create(ctx context.Context, text string) error {
	id, err := b.tg.SendMessage(ctx, b.msg.ChatID, text, keyboard(buttonIdle), true)
	if err != nil {
		return fmt.Errorf("send status message: %w", err)
	}
	b.msg.MessageID = id
	b.lastText = text
	b.log.Info("status message created", "message_id", id)
	return state.SaveJSON(b.opt.MessagePath, &b.msg)
}

// ---- updates ----

func (b *Bot) poll(ctx context.Context) error {
	offset, err := state.LoadInt(b.opt.OffsetPath)
	if err != nil {
		return err
	}
	for ctx.Err() == nil {
		ups, err := b.tg.GetUpdates(ctx, offset, pollTimeout)
		if err != nil {
			if ctx.Err() != nil {
				break
			}
			b.log.Warn("getUpdates", "err", err)
			select {
			case <-ctx.Done():
			case <-time.After(pollRetryWait):
			}
			continue
		}
		for _, u := range ups {
			b.handle(ctx, u)
			offset = u.ID + 1
			if err := state.SaveInt(b.opt.OffsetPath, offset); err != nil {
				b.log.Warn("save offset", "err", err)
			}
		}
	}
	return ctx.Err()
}

func (b *Bot) handle(ctx context.Context, u telegram.Update) {
	switch {
	case u.CallbackQuery != nil:
		b.handleCallback(ctx, u.CallbackQuery)
	case u.Message != nil:
		b.handleMessage(ctx, u.Message)
	}
}

func (b *Bot) owner() int64 {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.msg.ChatID
}

func (b *Bot) handleMessage(ctx context.Context, m *telegram.Message) {
	cmd, _, _ := strings.Cut(strings.TrimSpace(m.Text), " ")
	cmd, _, _ = strings.Cut(cmd, "@")
	if m.Chat.Type != "private" || (cmd != "/start" && cmd != "/status") {
		return
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.msg.ChatID != 0 && b.msg.ChatID != m.Chat.ID {
		b.log.Warn("ignoring command from another chat", "chat_id", m.Chat.ID)
		return
	}
	if b.msg.ChatID == 0 {
		b.log.Info("adopting chat", "chat_id", m.Chat.ID)
		b.msg = message{ChatID: m.Chat.ID}
	}
	b.fetchAll(ctx, false)
	if err := b.publish(ctx, true); err != nil {
		b.log.Error("publish after command", "err", err)
	}
}

func (b *Bot) handleCallback(ctx context.Context, q *telegram.CallbackQuery) {
	ack := func(text string) {
		if err := b.tg.AnswerCallback(ctx, q.ID, text); err != nil {
			b.log.Debug("answer callback", "err", err)
		}
	}
	if q.Data != callbackRefresh && q.Data != callbackRefreshLegacy {
		ack("Unknown action")
		return
	}
	owner := b.owner()
	if q.Message == nil || (owner != 0 && q.Message.Chat.ID != owner) {
		ack("Not allowed here")
		return
	}
	// Stop the spinner right away; the button itself shows progress.
	ack("")
	chat, msg := q.Message.Chat.ID, q.Message.ID
	if err := b.tg.EditMarkup(ctx, chat, msg, keyboard(buttonBusy)); err != nil && !telegram.IsNotModified(err) {
		b.log.Debug("set busy button", "err", err)
	}
	if err := b.Refresh(ctx, true); err != nil {
		b.log.Error("manual refresh", "err", err)
		if err := b.tg.EditMarkup(ctx, chat, msg, keyboard(buttonFailed)); err != nil && !telegram.IsNotModified(err) {
			b.log.Debug("set failed button", "err", err)
		}
		return
	}
	// The edit in Refresh restores the idle button only when the text changed;
	// make sure the button is idle regardless.
	if err := b.tg.EditMarkup(ctx, chat, msg, keyboard(buttonIdle)); err != nil && !telegram.IsNotModified(err) {
		b.log.Debug("restore button", "err", err)
	}
}
