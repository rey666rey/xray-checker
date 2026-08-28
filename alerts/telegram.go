package alerts

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"time"
)

const telegramAPIBase = "https://api.telegram.org"

type TelegramClient struct {
	baseURL    string
	httpClient *http.Client
}

type BotInfo struct {
	ID        int64  `json:"id"`
	Username  string `json:"username"`
	FirstName string `json:"firstName"`
}

type ChatInfo struct {
	ID       int64  `json:"id"`
	Label    string `json:"label"`
	Type     string `json:"type"`
	Username string `json:"username,omitempty"`
}

type telegramResponse struct {
	OK          bool            `json:"ok"`
	Result      json.RawMessage `json:"result"`
	Description string          `json:"description"`
}

type telegramUser struct {
	ID        int64  `json:"id"`
	Username  string `json:"username"`
	FirstName string `json:"first_name"`
}

type telegramChat struct {
	ID        int64  `json:"id"`
	Type      string `json:"type"`
	Title     string `json:"title"`
	Username  string `json:"username"`
	FirstName string `json:"first_name"`
	LastName  string `json:"last_name"`
}

type telegramUpdate struct {
	UpdateID int64 `json:"update_id"`
	Message  *struct {
		Text string       `json:"text"`
		Chat telegramChat `json:"chat"`
	} `json:"message"`
}

func NewTelegramClient() *TelegramClient {
	return &TelegramClient{
		baseURL: telegramAPIBase,
		httpClient: &http.Client{
			Timeout: 12 * time.Second,
		},
	}
}

func (c *TelegramClient) withHTTPClient(httpClient *http.Client) *TelegramClient {
	return &TelegramClient{baseURL: c.baseURL, httpClient: httpClient}
}

type telegramCallError struct {
	message   string
	retryable bool
}

func (e *telegramCallError) Error() string { return e.message }

func telegramErrorRetryable(err error) bool {
	var callErr *telegramCallError
	if errors.As(err, &callErr) {
		return callErr.retryable
	}
	return false
}

func (c *TelegramClient) Verify(ctx context.Context, token string) (BotInfo, error) {
	if err := validateToken(token); err != nil {
		return BotInfo{}, err
	}
	var user telegramUser
	if err := c.call(ctx, token, "getMe", nil, &user); err != nil {
		return BotInfo{}, err
	}
	return BotInfo{ID: user.ID, Username: user.Username, FirstName: user.FirstName}, nil
}

func (c *TelegramClient) DiscoverChats(ctx context.Context, token string) ([]ChatInfo, error) {
	if err := validateToken(token); err != nil {
		return nil, err
	}
	request := map[string]interface{}{
		"timeout":         0,
		"limit":           100,
		"allowed_updates": []string{"message"},
	}
	var updates []telegramUpdate
	if err := c.call(ctx, token, "getUpdates", request, &updates); err != nil {
		return nil, err
	}

	byID := make(map[int64]ChatInfo)
	for _, update := range updates {
		if update.Message == nil || !strings.HasPrefix(strings.TrimSpace(update.Message.Text), "/start") {
			continue
		}
		chat := update.Message.Chat
		label := strings.TrimSpace(strings.Join([]string{chat.FirstName, chat.LastName}, " "))
		if label == "" {
			label = strings.TrimSpace(chat.Title)
		}
		if label == "" && chat.Username != "" {
			label = "@" + chat.Username
		}
		if label == "" {
			label = fmt.Sprintf("Chat %d", chat.ID)
		}
		byID[chat.ID] = ChatInfo{ID: chat.ID, Label: label, Type: chat.Type, Username: chat.Username}
	}

	chats := make([]ChatInfo, 0, len(byID))
	for _, chat := range byID {
		chats = append(chats, chat)
	}
	sort.Slice(chats, func(i, j int) bool {
		if chats[i].Type != chats[j].Type {
			return chats[i].Type < chats[j].Type
		}
		return chats[i].Label < chats[j].Label
	})
	return chats, nil
}

func (c *TelegramClient) SendMessage(ctx context.Context, token string, chatID int64, text string) error {
	if err := validateToken(token); err != nil {
		return err
	}
	if chatID == 0 {
		return errors.New("Telegram recipient is not configured")
	}
	request := map[string]interface{}{
		"chat_id":                  chatID,
		"text":                     text,
		"parse_mode":               "HTML",
		"disable_web_page_preview": true,
	}
	return c.call(ctx, token, "sendMessage", request, nil)
}

func (c *TelegramClient) call(ctx context.Context, token, method string, payload interface{}, result interface{}) error {
	var body io.Reader
	if payload != nil {
		encoded, err := json.Marshal(payload)
		if err != nil {
			return fmt.Errorf("encode Telegram request: %w", err)
		}
		body = bytes.NewReader(encoded)
	}

	endpoint := strings.TrimRight(c.baseURL, "/") + "/bot" + url.PathEscape(token) + "/" + method
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, body)
	if err != nil {
		return fmt.Errorf("create Telegram request: %w", err)
	}
	request.Header.Set("Content-Type", "application/json")

	response, err := c.httpClient.Do(request)
	if err != nil {
		return &telegramCallError{message: fmt.Sprintf("Telegram is unavailable: %v", err), retryable: true}
	}
	defer response.Body.Close()

	data, err := io.ReadAll(io.LimitReader(response.Body, 1024*1024))
	if err != nil {
		return &telegramCallError{message: fmt.Sprintf("read Telegram response: %v", err), retryable: true}
	}
	var envelope telegramResponse
	if err := json.Unmarshal(data, &envelope); err != nil {
		return &telegramCallError{message: "Telegram returned an invalid response", retryable: true}
	}
	if !envelope.OK {
		description := strings.TrimSpace(envelope.Description)
		if description == "" {
			description = fmt.Sprintf("HTTP %d", response.StatusCode)
		}
		return &telegramCallError{message: fmt.Sprintf("Telegram rejected the request: %s", description), retryable: false}
	}
	if result != nil && len(envelope.Result) > 0 {
		if err := json.Unmarshal(envelope.Result, result); err != nil {
			return fmt.Errorf("decode Telegram response: %w", err)
		}
	}
	return nil
}

func validateToken(token string) error {
	token = strings.TrimSpace(token)
	if token == "" {
		return errors.New("Telegram bot token is required")
	}
	if len(token) > 256 || strings.ContainsAny(token, "/?#") {
		return errors.New("Telegram bot token has an invalid format")
	}
	parts := strings.Split(token, ":")
	if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
		return errors.New("Telegram bot token has an invalid format")
	}
	for _, char := range parts[0] {
		if char < '0' || char > '9' {
			return errors.New("Telegram bot token has an invalid format")
		}
	}
	return nil
}
