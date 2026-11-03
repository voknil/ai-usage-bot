package bot

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/voknil/ai-usage-bot/internal/telegram"
	"github.com/voknil/ai-usage-bot/internal/usage"
)

// fakeTelegram is an in-memory Bot API.
type fakeTelegram struct {
	t  *testing.T
	mu sync.Mutex

	nextID   int64
	messages map[int64]string // id -> text
	markup   map[int64]string
	calls    []string
	answers  []string
	updates  []telegram.Update
}

func newFake(t *testing.T) (*fakeTelegram, *telegram.Client) {
	f := &fakeTelegram{t: t, nextID: 100, messages: map[int64]string{}, markup: map[int64]string{}}
	srv := httptest.NewServer(http.HandlerFunc(f.serve))
	t.Cleanup(srv.Close)
	c := telegram.New("1:tok")
	c.Base = srv.URL
	return f, c
}

func (f *fakeTelegram) serve(w http.ResponseWriter, r *http.Request) {
	_ = r.ParseForm()
	method := r.URL.Path[strings.LastIndex(r.URL.Path, "/")+1:]
	form := r.PostForm

	f.mu.Lock()
	defer f.mu.Unlock()
	if method != "getUpdates" {
		f.calls = append(f.calls, method)
	}
	ok := func(result any) {
		_ = json.NewEncoder(w).Encode(map[string]any{"ok": true, "result": result})
	}
	fail := func(desc string) {
		_ = json.NewEncoder(w).Encode(map[string]any{"ok": false, "error_code": 400, "description": desc})
	}
	id, _ := strconv.ParseInt(form.Get("message_id"), 10, 64)

	switch method {
	case "sendMessage":
		f.nextID++
		f.messages[f.nextID] = form.Get("text")
		f.markup[f.nextID] = form.Get("reply_markup")
		ok(map[string]any{"message_id": f.nextID, "chat": map[string]any{"id": 1}})
	case "editMessageText":
		old, exists := f.messages[id]
		switch {
		case !exists:
			fail("Bad Request: message to edit not found")
		case old == form.Get("text") && f.markup[id] == form.Get("reply_markup"):
			fail("Bad Request: message is not modified")
		default:
			f.messages[id], f.markup[id] = form.Get("text"), form.Get("reply_markup")
			ok(true)
		}
	case "editMessageReplyMarkup":
		if _, exists := f.messages[id]; !exists {
			fail("Bad Request: message to edit not found")
			return
		}
		f.markup[id] = form.Get("reply_markup")
		ok(true)
	case "deleteMessage":
		if _, exists := f.messages[id]; !exists {
			fail("Bad Request: message to delete not found")
			return
		}
		delete(f.messages, id)
		ok(true)
	case "answerCallbackQuery":
		f.answers = append(f.answers, form.Get("text"))
		ok(true)
	case "getUpdates":
		ups := f.updates
		f.updates = nil
		if len(ups) == 0 {
			f.mu.Unlock()
			time.Sleep(20 * time.Millisecond)
			f.mu.Lock()
		}
		ok(ups)
	default:
		fail("unknown method " + method)
	}
}

func (f *fakeTelegram) count(method string) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	n := 0
	for _, c := range f.calls {
		if c == method {
			n++
		}
	}
	return n
}

func (f *fakeTelegram) texts() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []string
	for _, v := range f.messages {
		out = append(out, v)
	}
	return out
}

type harness struct {
	bot     *Bot
	fake    *fakeTelegram
	dir     string
	claude  *usage.Claude
	fetches int
	failing bool
	now     time.Time
}

func newHarness(t *testing.T, chatID int64) *harness {
	t.Helper()
	fake, tg := newFake(t)
	h := &harness{fake: fake, dir: t.TempDir(), now: time.Date(2026, 11, 3, 9, 25, 0, 0, time.UTC)}
	h.claude = &usage.Claude{
		CapturedAt: h.now,
		Session:    &usage.Window{Label: "Current session", Percent: 20, ResetsAt: h.now.Add(2 * time.Hour)},
		Week:       &usage.Window{Label: "All models", Percent: 30, ResetsAt: h.now.Add(48 * time.Hour)},
	}
	src := Sources{Claude: func(context.Context) (*usage.Claude, error) {
		h.fetches++
		if h.failing {
			return nil, errors.New("boom")
		}
		c := *h.claude
		return &c, nil
	}}
	b, err := New(tg, src, Options{
		ChatID:          chatID,
		RefreshInterval: time.Hour,
		CacheTTL:        4 * time.Minute,
		Thresholds:      []float64{85, 95},
		MessagePath:     filepath.Join(h.dir, "msg.json"),
		AlertPath:       filepath.Join(h.dir, "alerts.json"),
		OffsetPath:      filepath.Join(h.dir, "offset"),
		Location:        time.UTC,
		Now:             func() time.Time { return h.now },
	}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatal(err)
	}
	h.bot = b
	return h
}

func TestNoChatMeansNoTelegramTraffic(t *testing.T) {
	h := newHarness(t, 0)
	if err := h.bot.Refresh(context.Background(), false); err != nil {
		t.Fatal(err)
	}
	if h.fake.count("sendMessage") != 0 {
		t.Fatal("must not send before a chat is known")
	}
}

func TestCreateThenEditOnlyOnChange(t *testing.T) {
	ctx := context.Background()
	h := newHarness(t, 1)
	must(t, h.bot.Refresh(ctx, false))
	if h.fake.count("sendMessage") != 1 || h.fake.count("editMessageText") != 0 {
		t.Fatalf("first refresh: calls = %v", h.fake.calls)
	}
	// Cached and unchanged: no fetch, no edit.
	must(t, h.bot.Refresh(ctx, false))
	if h.fetches != 1 || h.fake.count("editMessageText") != 0 {
		t.Fatalf("second refresh: fetches=%d calls=%v", h.fetches, h.fake.calls)
	}
	// New data after the TTL: one edit.
	h.now = h.now.Add(5 * time.Minute)
	h.claude.CapturedAt = h.now
	h.claude.Session.Percent = 25
	must(t, h.bot.Refresh(ctx, false))
	if h.fetches != 2 || h.fake.count("editMessageText") != 1 {
		t.Fatalf("third refresh: fetches=%d calls=%v", h.fetches, h.fake.calls)
	}
	if got := h.fake.texts(); len(got) != 1 || !strings.HasPrefix(got[0], "5h: 25% used") {
		t.Fatalf("message text: %q", got)
	}
}

func TestMessageGoneIsRecreatedAndPersisted(t *testing.T) {
	ctx := context.Background()
	h := newHarness(t, 1)
	must(t, h.bot.Refresh(ctx, false))
	h.fake.mu.Lock()
	h.fake.messages = map[int64]string{} // user deleted the message
	h.fake.mu.Unlock()

	h.now = h.now.Add(5 * time.Minute)
	h.claude.CapturedAt = h.now
	must(t, h.bot.Refresh(ctx, false))
	if h.fake.count("sendMessage") != 2 || len(h.fake.texts()) != 1 {
		t.Fatalf("calls = %v", h.fake.calls)
	}
	var saved message
	data := readFile(t, filepath.Join(h.dir, "msg.json"))
	_ = json.Unmarshal(data, &saved)
	if saved.MessageID != 102 {
		t.Errorf("new message ID not persisted: %+v", saved)
	}
}

func TestSourceFailureKeepsPreviousValue(t *testing.T) {
	ctx := context.Background()
	h := newHarness(t, 1)
	must(t, h.bot.Refresh(ctx, false))
	h.failing = true
	h.now = h.now.Add(10 * time.Minute)
	must(t, h.bot.Refresh(ctx, false)) // must not error or blank the message
	if got := h.fake.texts(); len(got) != 1 || !strings.Contains(got[0], "5h: 20% used") {
		t.Fatalf("old data should remain: %q", got)
	}
}

func TestThresholdAlertsAndRetraction(t *testing.T) {
	ctx := context.Background()
	h := newHarness(t, 1)
	must(t, h.bot.Refresh(ctx, false))

	h.now = h.now.Add(5 * time.Minute)
	h.claude.CapturedAt = h.now
	h.claude.Session.Percent = 90
	must(t, h.bot.Refresh(ctx, false))
	if got := h.fake.count("sendMessage"); got != 2 { // status + one alert
		t.Fatalf("expected one alert, calls=%v", h.fake.calls)
	}
	alertFound := false
	for _, txt := range h.fake.texts() {
		if strings.HasPrefix(txt, "⚠️ Claude 5h: 85% used") {
			alertFound = true
		}
	}
	if !alertFound {
		t.Fatalf("alert text missing: %q", h.fake.texts())
	}

	// Same level again: no second alert.
	h.now = h.now.Add(5 * time.Minute)
	h.claude.CapturedAt = h.now
	must(t, h.bot.Refresh(ctx, false))
	if h.fake.count("sendMessage") != 2 {
		t.Fatalf("alert repeated: %v", h.fake.calls)
	}

	// Window resets: the alert is deleted.
	h.now = h.now.Add(5 * time.Minute)
	h.claude.CapturedAt = h.now
	h.claude.Session.Percent = 2
	must(t, h.bot.Refresh(ctx, false))
	if h.fake.count("deleteMessage") != 1 {
		t.Fatalf("alert not retracted: %v", h.fake.calls)
	}
	for _, txt := range h.fake.texts() {
		if strings.HasPrefix(txt, "⚠️") {
			t.Errorf("alert still present: %q", txt)
		}
	}
}

func TestCallbackAuthorisationAndForcedRefresh(t *testing.T) {
	ctx := context.Background()
	h := newHarness(t, 1)
	must(t, h.bot.Refresh(ctx, false))
	statusID := int64(101)

	press := func(chat int64, data string) {
		h.bot.handle(ctx, telegram.Update{CallbackQuery: &telegram.CallbackQuery{
			ID: "cb", Data: data,
			Message: &telegram.Message{ID: statusID, Chat: telegram.Chat{ID: chat, Type: "private"}},
		}})
	}

	press(999, callbackRefresh)
	if h.fetches != 1 || h.fake.answers[len(h.fake.answers)-1] != "Not allowed here" {
		t.Fatalf("foreign chat must be refused: fetches=%d answers=%v", h.fetches, h.fake.answers)
	}
	press(1, "something_else")
	if h.fetches != 1 || h.fake.answers[len(h.fake.answers)-1] != "Unknown action" {
		t.Fatalf("unknown action: answers=%v", h.fake.answers)
	}

	press(1, callbackRefresh) // forced: refetches although the cache is fresh
	if h.fetches != 2 {
		t.Fatalf("a button press must force a fetch, fetches=%d", h.fetches)
	}
	if h.fake.markup[statusID] != keyboard(buttonIdle) {
		t.Errorf("button not restored: %s", h.fake.markup[statusID])
	}

	press(1, callbackRefreshLegacy) // buttons from the shell version keep working
	if h.fetches != 3 {
		t.Fatalf("legacy callback ignored, fetches=%d", h.fetches)
	}
}

func TestStartAdoptsFirstChatOnly(t *testing.T) {
	ctx := context.Background()
	h := newHarness(t, 0)
	start := func(chat int64, typ string) {
		h.bot.handle(ctx, telegram.Update{Message: &telegram.Message{
			ID: 1, Text: "/start", Chat: telegram.Chat{ID: chat, Type: typ},
		}})
	}
	start(5, "group")
	if h.fake.count("sendMessage") != 0 {
		t.Fatal("groups must be ignored")
	}
	start(7, "private")
	if h.bot.owner() != 7 || h.fake.count("sendMessage") != 1 {
		t.Fatalf("owner=%d calls=%v", h.bot.owner(), h.fake.calls)
	}
	start(8, "private") // someone else
	if h.bot.owner() != 7 || h.fake.count("sendMessage") != 1 {
		t.Fatalf("second chat must be ignored: owner=%d calls=%v", h.bot.owner(), h.fake.calls)
	}
	start(7, "private") // owner again: board is replaced, not duplicated
	if len(h.fake.texts()) != 1 || h.fake.count("deleteMessage") != 1 {
		t.Fatalf("board must stay a single message: %v", h.fake.calls)
	}
}

func TestPinnedChatIgnoresStartElsewhere(t *testing.T) {
	h := newHarness(t, 1)
	h.bot.handle(context.Background(), telegram.Update{Message: &telegram.Message{
		Text: "/start", Chat: telegram.Chat{ID: 2, Type: "private"},
	}})
	if h.fake.count("sendMessage") != 0 {
		t.Fatal("a pinned bot must not answer other chats")
	}
}

func TestRunProcessesUpdatesAndStops(t *testing.T) {
	h := newHarness(t, 1)
	h.fake.mu.Lock()
	h.fake.updates = []telegram.Update{{ID: 41, CallbackQuery: &telegram.CallbackQuery{
		ID: "cb", Data: callbackRefresh,
		Message: &telegram.Message{ID: 101, Chat: telegram.Chat{ID: 1, Type: "private"}},
	}}}
	h.fake.mu.Unlock()

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- h.bot.Run(ctx) }()

	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if data := readFileQuiet(filepath.Join(h.dir, "offset")); strings.TrimSpace(string(data)) == "42" {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Run returned %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Run did not stop after cancel")
	}
	if got := strings.TrimSpace(string(readFile(t, filepath.Join(h.dir, "offset")))); got != "42" {
		t.Errorf("offset = %q, want 42", got)
	}
}

func must(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}

func readFile(t *testing.T, path string) []byte {
	t.Helper()
	data := readFileQuiet(path)
	if data == nil {
		t.Fatalf("cannot read %s", path)
	}
	return data
}

var _ = fmt.Sprint

func readFileQuiet(path string) []byte {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	return data
}
