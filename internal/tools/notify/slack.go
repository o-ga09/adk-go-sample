// Package notifytools (this file) exposes a Slack notification tool backed by
// an Incoming Webhook. This is now the primary notification channel; see
// line.go for the LINE Messaging API tool kept alongside it.
package notifytools

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"

	"github.com/o-ga09/adk-go-sample/internal/config"
	"google.golang.org/adk/tool"
	"google.golang.org/adk/tool/functiontool"
)

// SlackTools returns the Slack notification tool.
func SlackTools(c *config.Config) ([]tool.Tool, error) {
	pushTool, err := functiontool.New(functiontool.Config{
		Name:        "slack_push",
		Description: "Send a text notification to the user via Slack. Use this to deliver the final summary.",
	}, slackPush(c))
	if err != nil {
		return nil, err
	}
	return []tool.Tool{pushTool}, nil
}

type slackPushInput struct {
	Message string `json:"message"`
}

type slackPushResult struct {
	Status string `json:"status"`
	Error  string `json:"error,omitempty"`
}

type slackWebhookBody struct {
	Text string `json:"text"`
}

func slackPush(c *config.Config) functiontool.Func[slackPushInput, slackPushResult] {
	return func(ctx tool.Context, in slackPushInput) slackPushResult {
		if c.SlackWebhookURL == "" {
			return slackPushResult{Status: "skipped", Error: "Slack not configured"}
		}
		body, _ := json.Marshal(slackWebhookBody{Text: truncate(in.Message, 39900)})
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.SlackWebhookURL, bytes.NewReader(body))
		if err != nil {
			return slackPushResult{Status: "error", Error: err.Error()}
		}
		req.Header.Set("Content-Type", "application/json")

		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			return slackPushResult{Status: "error", Error: err.Error()}
		}
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			b, _ := io.ReadAll(resp.Body)
			return slackPushResult{Status: "error", Error: fmt.Sprintf("slack webhook %d: %s", resp.StatusCode, string(b))}
		}
		return slackPushResult{Status: "sent"}
	}
}
