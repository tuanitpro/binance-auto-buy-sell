package notifier

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
)

// TelegramNotifier handles sending messages via Telegram Bot API.
type TelegramNotifier struct {
	BotToken string
	ChatID   string
}

// NewTelegramNotifier creates a new instance of TelegramNotifier.
func NewTelegramNotifier(botToken, chatID string) *TelegramNotifier {
	return &TelegramNotifier{
		BotToken: botToken,
		ChatID:   chatID,
	}
}

// Send sends a plain text message.
func (t *TelegramNotifier) Send(message string) error {
	url := fmt.Sprintf("https://api.telegram.org/bot%s/sendMessage", t.BotToken)

	payload := map[string]interface{}{
		"chat_id":    t.ChatID,
		"text":       message,
		"parse_mode": "Markdown", // optional: allows *bold*, _italic_, etc.
	}

	body, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("failed to marshal Telegram payload: %w", err)
	}

	resp, err := http.Post(url, "application/json", bytes.NewBuffer(body))
	if err != nil {
		return fmt.Errorf("failed to send Telegram message: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("telegram API returned status %d", resp.StatusCode)
	}

	return nil
}
