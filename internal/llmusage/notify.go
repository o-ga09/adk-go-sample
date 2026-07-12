package llmusage

import (
	"context"
	"fmt"

	"github.com/o-ga09/adk-go-sample/internal/slackfmt"
	"github.com/slack-go/slack"
)

// PostToSlack posts blocks to channel using a Slack Bot Token client. Unlike
// the agent's own notify tool (internal/tools/notify), the daily cost report
// is posted directly by the batch process without going through the LLM —
// calling the model to report on the model's own cost would be circular.
// When exceeded is true (the report's cost passed its alert threshold),
// blocks are wrapped in a red-bordered attachment (slackfmt.ColoredAttachment)
// so the overrun stands out more than a normal report.
func PostToSlack(ctx context.Context, token, channel string, blocks []slack.Block, exceeded bool) error {
	if token == "" || channel == "" {
		return fmt.Errorf("slack not configured: SLACK_BOT_TOKEN/SLACK_CHANNEL_ID unset")
	}
	client := slack.New(token)
	var opt slack.MsgOption
	if exceeded {
		opt = slack.MsgOptionAttachments(slackfmt.ColoredAttachment("danger", blocks...))
	} else {
		opt = slack.MsgOptionBlocks(blocks...)
	}
	_, _, err := client.PostMessageContext(ctx, channel, opt)
	return err
}
