package llmusage

import (
	"context"
	"fmt"

	"github.com/slack-go/slack"
)

// PostToSlack posts text to channel using a Slack Bot Token client. Unlike
// the agent's own notify tool (internal/tools/notify), the daily cost report
// is posted directly by the batch process without going through the LLM —
// calling the model to report on the model's own cost would be circular.
func PostToSlack(ctx context.Context, token, channel, text string) error {
	if token == "" || channel == "" {
		return fmt.Errorf("slack not configured: SLACK_BOT_TOKEN/SLACK_CHANNEL_ID unset")
	}
	client := slack.New(token)
	_, _, err := client.PostMessageContext(ctx, channel, slack.MsgOptionText(text, false))
	return err
}
