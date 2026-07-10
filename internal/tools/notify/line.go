// Package notifytools exposes notification tools (Slack, LINE) used by the
// gmail agent to deliver its summary. This file is the LINE push-notification
// tool; it uses the LINE Messaging API push endpoint (the old LINE Notify
// service was shut down on 2025-03-31). LINE is kept as a fallback channel
// alongside Slack (see slack.go), which is the default.
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

const pushURL = "https://api.line.me/v2/bot/message/push"

// LineTools returns the LINE notification tool.
func LineTools(c *config.Config) ([]tool.Tool, error) {
	pushTool, err := functiontool.New(functiontool.Config{
		Name:        "line_push",
		Description: "Send a text notification to the user via LINE. Use this to deliver the final summary.",
	}, push(c))
	if err != nil {
		return nil, err
	}
	return []tool.Tool{pushTool}, nil
}

type pushInput struct {
	Message string `json:"message"`
}

type pushResult struct {
	Status string `json:"status"`
	Error  string `json:"error,omitempty"`
}

type lineMessage struct {
	Type string `json:"type"`
	Text string `json:"text"`
}

type linePushBody struct {
	To       string        `json:"to"`
	Messages []lineMessage `json:"messages"`
}

func push(c *config.Config) functiontool.Func[pushInput, pushResult] {
	return func(ctx tool.Context, in pushInput) pushResult {
		if c.LineChannelToken == "" || c.LineTargetUserID == "" {
			return pushResult{Status: "skipped", Error: "LINE not configured"}
		}
		body, _ := json.Marshal(linePushBody{
			To: c.LineTargetUserID,
			Messages: []lineMessage{
				{Type: "text", Text: truncate(in.Message, 4900)},
			},
		})
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, pushURL, bytes.NewReader(body))
		if err != nil {
			return pushResult{Status: "error", Error: err.Error()}
		}
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Authorization", "Bearer "+c.LineChannelToken)

		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			return pushResult{Status: "error", Error: err.Error()}
		}
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			b, _ := io.ReadAll(resp.Body)
			return pushResult{Status: "error", Error: fmt.Sprintf("line api %d: %s", resp.StatusCode, string(b))}
		}
		return pushResult{Status: "sent"}
	}
}

func truncate(s string, max int) string {
	if len(s) <= max {
		return s
	}
	return s[:max]
}
