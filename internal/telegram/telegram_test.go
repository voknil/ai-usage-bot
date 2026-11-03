package telegram

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"
)

func fakeAPI(t *testing.T, h func(method string, form url.Values) (status int, body string)) *Client {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		method := r.URL.Path[strings.LastIndex(r.URL.Path, "/")+1:]
		if !strings.HasPrefix(r.URL.Path, "/bot123:abc/") {
			t.Errorf("unexpected path %s", r.URL.Path)
		}
		status, body := h(method, r.PostForm)
		w.WriteHeader(status)
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(srv.Close)
	c := New("123:abc")
	c.Base = srv.URL
	return c
}

func TestSendAndEdit(t *testing.T) {
	c := fakeAPI(t, func(m string, f url.Values) (int, string) {
		switch m {
		case "sendMessage":
			if f.Get("chat_id") != "42" || f.Get("text") != "hi" || f.Get("disable_notification") != "true" ||
				!strings.Contains(f.Get("reply_markup"), "refresh") {
				t.Errorf("bad sendMessage form: %v", f)
			}
			return 200, `{"ok":true,"result":{"message_id":7,"chat":{"id":42}}}`
		case "editMessageText":
			return 200, `{"ok":true,"result":true}`
		}
		return 404, `{"ok":false}`
	})
	id, err := c.SendMessage(context.Background(), 42, "hi", Keyboard(Button{"x", "refresh"}), true)
	if err != nil || id != 7 {
		t.Fatalf("id=%d err=%v", id, err)
	}
	if err := c.EditMessage(context.Background(), 42, 7, "new", ""); err != nil {
		t.Fatal(err)
	}
}

func TestErrorClassification(t *testing.T) {
	c := fakeAPI(t, func(m string, _ url.Values) (int, string) {
		switch m {
		case "editMessageText":
			return 400, `{"ok":false,"error_code":400,"description":"Bad Request: message is not modified: specified new message content..."}`
		case "deleteMessage":
			return 400, `{"ok":false,"error_code":400,"description":"Bad Request: message to delete not found"}`
		}
		return 403, `{"ok":false,"error_code":403,"description":"Forbidden: bot was blocked by the user"}`
	})
	if err := c.EditMessage(context.Background(), 1, 1, "t", ""); !IsNotModified(err) || IsGone(err) {
		t.Errorf("not modified misclassified: %v", err)
	}
	if err := c.DeleteMessage(context.Background(), 1, 1); !IsGone(err) {
		t.Errorf("gone misclassified: %v", err)
	}
	_, err := c.SendMessage(context.Background(), 1, "t", "", false)
	if err == nil || IsGone(err) || IsNotModified(err) {
		t.Errorf("forbidden misclassified: %v", err)
	}
}

func TestRetryAfter(t *testing.T) {
	var calls atomic.Int32
	c := fakeAPI(t, func(string, url.Values) (int, string) {
		if calls.Add(1) == 1 {
			return 429, `{"ok":false,"error_code":429,"description":"Too Many Requests","parameters":{"retry_after":0}}`
		}
		return 200, `{"ok":true,"result":true}`
	})
	// retry_after 0 is not retried (treated as unknown): the 429 surfaces.
	if err := c.DeleteMessage(context.Background(), 1, 1); err == nil {
		t.Fatal("429 without retry_after must surface")
	}
}

func TestGetUpdates(t *testing.T) {
	c := fakeAPI(t, func(m string, f url.Values) (int, string) {
		if f.Get("offset") != "5" || !strings.Contains(f.Get("allowed_updates"), "callback_query") {
			t.Errorf("bad getUpdates form: %v", f)
		}
		return 200, `{"ok":true,"result":[
			{"update_id":5,"message":{"message_id":1,"chat":{"id":9,"type":"private"},"text":"/start"}},
			{"update_id":6,"callback_query":{"id":"cb","data":"refresh","message":{"message_id":2,"chat":{"id":9,"type":"private"}}}}]}`
	})
	ups, err := c.GetUpdates(context.Background(), 5, 0)
	if err != nil || len(ups) != 2 || ups[0].Message.Text != "/start" || ups[1].CallbackQuery.Data != "refresh" ||
		ups[1].CallbackQuery.Message.Chat.ID != 9 {
		t.Fatalf("got %+v, %v", ups, err)
	}
}

func TestTokenNeverInErrors(t *testing.T) {
	c := New("123:secret-token")
	c.Base = "http://127.0.0.1:1" // refused
	_, err := c.SendMessage(context.Background(), 1, "x", "", false)
	if err == nil {
		t.Fatal("expected a connection error")
	}
	if strings.Contains(err.Error(), "secret-token") {
		t.Fatalf("error leaks the token: %v", err)
	}
}

func TestKeyboard(t *testing.T) {
	got := Keyboard(Button{"A", "a"})
	if got != `{"inline_keyboard":[[{"text":"A","callback_data":"a"}]]}` {
		t.Errorf("keyboard = %s", got)
	}
}
