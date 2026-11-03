// Package telegram is a minimal Bot API client: just the calls this bot needs.
package telegram

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// Client talks to the Bot API.
type Client struct {
	token string
	// Base is the API root, overridable for tests.
	Base string
	HTTP *http.Client
}

// New returns a Client. The HTTP timeout leaves room for long polling.
func New(token string) *Client {
	return &Client{
		token: token,
		Base:  "https://api.telegram.org",
		HTTP:  &http.Client{Timeout: 70 * time.Second},
	}
}

// APIError is an error reported by Telegram.
type APIError struct {
	Method      string
	Code        int
	Description string
	RetryAfter  int
}

func (e *APIError) Error() string {
	return fmt.Sprintf("telegram %s: %d %s", e.Method, e.Code, e.Description)
}

// IsNotModified reports Telegram's "message is not modified" answer, which
// means the edit was a no-op rather than a failure.
func IsNotModified(err error) bool {
	var ae *APIError
	return errors.As(err, &ae) && strings.Contains(ae.Description, "message is not modified")
}

// IsGone reports that the message to edit or delete no longer exists.
func IsGone(err error) bool {
	var ae *APIError
	if !errors.As(err, &ae) {
		return false
	}
	return strings.Contains(ae.Description, "message to edit not found") ||
		strings.Contains(ae.Description, "message to delete not found") ||
		strings.Contains(ae.Description, "MESSAGE_ID_INVALID")
}

// Button is one inline keyboard button.
type Button struct {
	Text         string `json:"text"`
	CallbackData string `json:"callback_data"`
}

// Keyboard builds a single-row inline keyboard marker.
func Keyboard(b ...Button) string {
	data, _ := json.Marshal(map[string]any{"inline_keyboard": [][]Button{b}})
	return string(data)
}

// Update is the subset of an incoming update the bot reads.
type Update struct {
	ID            int64          `json:"update_id"`
	Message       *Message       `json:"message"`
	CallbackQuery *CallbackQuery `json:"callback_query"`
}

// Message is an incoming message.
type Message struct {
	ID   int64  `json:"message_id"`
	Chat Chat   `json:"chat"`
	Text string `json:"text"`
}

// Chat identifies a chat.
type Chat struct {
	ID   int64  `json:"id"`
	Type string `json:"type"`
}

// CallbackQuery is a button press.
type CallbackQuery struct {
	ID      string   `json:"id"`
	Data    string   `json:"data"`
	Message *Message `json:"message"`
}

type response struct {
	OK          bool            `json:"ok"`
	Result      json.RawMessage `json:"result"`
	Code        int             `json:"error_code"`
	Description string          `json:"description"`
	Parameters  struct {
		RetryAfter int `json:"retry_after"`
	} `json:"parameters"`
}

// call performs a Bot API method. It retries once after a 429 when Telegram's
// retry_after is short. The token is part of the request URL by Telegram's
// design, so errors are built without it.
func (c *Client) call(ctx context.Context, method string, params url.Values, out any) error {
	for attempt := 0; ; attempt++ {
		err := c.callOnce(ctx, method, params, out)
		var ae *APIError
		if attempt == 0 && errors.As(err, &ae) && ae.Code == http.StatusTooManyRequests &&
			ae.RetryAfter > 0 && ae.RetryAfter <= 10 {
			select {
			case <-time.After(time.Duration(ae.RetryAfter) * time.Second):
				continue
			case <-ctx.Done():
				return ctx.Err()
			}
		}
		return err
	}
}

func (c *Client) callOnce(ctx context.Context, method string, params url.Values, out any) error {
	endpoint := c.Base + "/bot" + c.token + "/" + method
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, strings.NewReader(params.Encode()))
	if err != nil {
		return fmt.Errorf("telegram %s: %w", method, errNoToken(err, c.token))
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return fmt.Errorf("telegram %s: %w", method, errNoToken(err, c.token))
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if err != nil {
		return fmt.Errorf("telegram %s: read: %w", method, err)
	}
	var r response
	if err := json.Unmarshal(body, &r); err != nil {
		return fmt.Errorf("telegram %s: HTTP %d, unreadable response", method, resp.StatusCode)
	}
	if !r.OK {
		return &APIError{Method: method, Code: r.Code, Description: r.Description, RetryAfter: r.Parameters.RetryAfter}
	}
	if out != nil {
		return json.Unmarshal(r.Result, out)
	}
	return nil
}

// errNoToken strips the bot token from transport errors, which embed the URL.
func errNoToken(err error, token string) error {
	if token == "" {
		return err
	}
	var ue *url.Error
	if errors.As(err, &ue) {
		return fmt.Errorf("%s: %w", ue.Op, ue.Err)
	}
	return errors.New(strings.ReplaceAll(err.Error(), token, "<token>"))
}

// SendMessage posts text and returns the new message ID. markup may be empty.
func (c *Client) SendMessage(ctx context.Context, chatID int64, text, markup string, silent bool) (int64, error) {
	v := url.Values{
		"chat_id":              {strconv.FormatInt(chatID, 10)},
		"text":                 {text},
		"disable_notification": {strconv.FormatBool(silent)},
	}
	if markup != "" {
		v.Set("reply_markup", markup)
	}
	var m Message
	if err := c.call(ctx, "sendMessage", v, &m); err != nil {
		return 0, err
	}
	return m.ID, nil
}

// EditMessage replaces a message's text and keyboard.
func (c *Client) EditMessage(ctx context.Context, chatID, messageID int64, text, markup string) error {
	v := url.Values{
		"chat_id":    {strconv.FormatInt(chatID, 10)},
		"message_id": {strconv.FormatInt(messageID, 10)},
		"text":       {text},
	}
	if markup != "" {
		v.Set("reply_markup", markup)
	}
	return c.call(ctx, "editMessageText", v, nil)
}

// EditMarkup replaces only the keyboard.
func (c *Client) EditMarkup(ctx context.Context, chatID, messageID int64, markup string) error {
	return c.call(ctx, "editMessageReplyMarkup", url.Values{
		"chat_id":      {strconv.FormatInt(chatID, 10)},
		"message_id":   {strconv.FormatInt(messageID, 10)},
		"reply_markup": {markup},
	}, nil)
}

// DeleteMessage removes a message.
func (c *Client) DeleteMessage(ctx context.Context, chatID, messageID int64) error {
	return c.call(ctx, "deleteMessage", url.Values{
		"chat_id":    {strconv.FormatInt(chatID, 10)},
		"message_id": {strconv.FormatInt(messageID, 10)},
	}, nil)
}

// AnswerCallback acknowledges a button press with an optional toast.
func (c *Client) AnswerCallback(ctx context.Context, id, text string) error {
	return c.call(ctx, "answerCallbackQuery", url.Values{
		"callback_query_id": {id},
		"text":              {text},
	}, nil)
}

// GetUpdates long-polls for updates after offset.
func (c *Client) GetUpdates(ctx context.Context, offset int64, timeoutSec int) ([]Update, error) {
	var ups []Update
	err := c.call(ctx, "getUpdates", url.Values{
		"offset":          {strconv.FormatInt(offset, 10)},
		"timeout":         {strconv.Itoa(timeoutSec)},
		"allowed_updates": {`["message","callback_query"]`},
	}, &ups)
	return ups, err
}
