package monitor

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"sync"
	"time"

	"github.com/dbs-rtf/agent/pkg/model"
)

type Notifier interface {
	Notify(event model.FailoverEvent) error
}

type LogNotifier struct {
	mu sync.Mutex
}

func NewLogNotifier() *LogNotifier {
	return &LogNotifier{}
}

func (n *LogNotifier) Notify(event model.FailoverEvent) error {
	n.mu.Lock()
	defer n.mu.Unlock()

	msg := fmt.Sprintf("[%s] HA Event: type=%s from=%s to=%s reason=%s result=%s",
		event.Timestamp.Format(time.RFC3339),
		event.EventType, event.From, event.To, event.Reason, event.Result)

	fmt.Println(msg)

	if len(event.Warnings) > 0 {
		for _, w := range event.Warnings {
			fmt.Printf("  WARNING: %s\n", w)
		}
	}

	return nil
}

type WebhookNotifier struct {
	url    string
	client *http.Client
	mu     sync.Mutex
}

func NewWebhookNotifier(url string) *WebhookNotifier {
	return &WebhookNotifier{
		url:    url,
		client: &http.Client{Timeout: 10 * time.Second},
	}
}

func (n *WebhookNotifier) Notify(event model.FailoverEvent) error {
	if n.url == "" {
		return nil
	}

	data, err := json.Marshal(event)
	if err != nil {
		return fmt.Errorf("marshal webhook event: %w", err)
	}

	n.mu.Lock()
	defer n.mu.Unlock()

	resp, err := n.client.Post(n.url, "application/json", bytes.NewReader(data))
	if err != nil {
		return fmt.Errorf("webhook POST failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("webhook returned status %d", resp.StatusCode)
	}

	return nil
}

type MultiNotifier struct {
	notifiers []Notifier
}

func NewMultiNotifier(notifiers ...Notifier) *MultiNotifier {
	return &MultiNotifier{notifiers: notifiers}
}

func (n *MultiNotifier) Notify(event model.FailoverEvent) error {
	var lastErr error
	for _, notifier := range n.notifiers {
		if err := notifier.Notify(event); err != nil {
			lastErr = err
		}
	}
	return lastErr
}
