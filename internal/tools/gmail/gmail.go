// Package gmailtools exposes Gmail operations to the agent as ADK function
// tools. The Gmail client is captured in a closure at construction time because
// ADK tool handlers only receive (tool.Context, TArgs).
package gmailtools

import (
	"encoding/base64"
	"fmt"
	"strings"

	"github.com/o-ga09/adk-go-sample/internal/config"
	"google.golang.org/adk/tool"
	"google.golang.org/adk/tool/functiontool"
	gmail "google.golang.org/api/gmail/v1"
)

const gmailUser = "me"

// Tools constructs every Gmail tool, wired to the given service and action mode.
// In dry_run mode the mutating tools log their intent but do not call the API.
func Tools(svc *gmail.Service, mode config.ActionMode) ([]tool.Tool, error) {
	listTool, err := functiontool.New(functiontool.Config{
		Name:        "gmail_list_messages",
		Description: "List messages matching a Gmail search query. Returns id, from, subject, snippet, and date for each message.",
	}, listMessages(svc))
	if err != nil {
		return nil, err
	}

	getTool, err := functiontool.New(functiontool.Config{
		Name:        "gmail_get_message",
		Description: "Get the full sender, subject, date and plain-text body of a single message by id.",
	}, getMessage(svc))
	if err != nil {
		return nil, err
	}

	ensureLabelTool, err := functiontool.New(functiontool.Config{
		Name:        "gmail_ensure_label",
		Description: "Ensure a Gmail label with the given name exists, creating it if necessary. Returns the label id. Idempotent.",
	}, ensureLabel(svc, mode))
	if err != nil {
		return nil, err
	}

	applyLabelTool, err := functiontool.New(functiontool.Config{
		Name:        "gmail_apply_label",
		Description: "Apply a label (by name) to a message. Optionally remove it from the inbox. Does NOT delete the message.",
	}, applyLabel(svc, mode))
	if err != nil {
		return nil, err
	}

	tools := []tool.Tool{listTool, getTool, ensureLabelTool, applyLabelTool}

	// Trashing is only available when explicitly enabled.
	if mode == config.ModeAutoTrash {
		trashTool, err := functiontool.New(functiontool.Config{
			Name:        "gmail_trash",
			Description: "Move a message to the trash (recoverable for 30 days). Only use for clearly unwanted mail.",
		}, trashMessage(svc, mode))
		if err != nil {
			return nil, err
		}
		tools = append(tools, trashTool)
	}

	return tools, nil
}

// ---- list ----

// NOTE: functiontool infers a JSON schema from these structs and validates
// every call/result against it. A field without `omitempty` is *required* and
// a nil slice serializes to null, which the schema rejects — either way the
// whole tool call fails at the ADK layer. Optional fields must carry
// `omitempty`. See .claude/rules/tool-json-schema.md.
type listInput struct {
	Query      string `json:"query"`
	MaxResults int64  `json:"maxResults,omitempty"`
}

type messageSummary struct {
	ID      string `json:"id"`
	From    string `json:"from"`
	Subject string `json:"subject"`
	Date    string `json:"date"`
	Snippet string `json:"snippet"`
}

type listResult struct {
	Messages []messageSummary `json:"messages,omitempty"`
	Status   string           `json:"status"`
	Error    string           `json:"error,omitempty"`
}

func listMessages(svc *gmail.Service) functiontool.Func[listInput, listResult] {
	return func(ctx tool.Context, in listInput) listResult {
		max := in.MaxResults
		if max <= 0 || max > 50 {
			max = 25
		}
		resp, err := svc.Users.Messages.List(gmailUser).Q(in.Query).MaxResults(max).Context(ctx).Do()
		if err != nil {
			return listResult{Status: "error", Error: err.Error()}
		}
		out := listResult{Status: "success"}
		for _, m := range resp.Messages {
			full, err := svc.Users.Messages.Get(gmailUser, m.Id).
				Format("metadata").
				MetadataHeaders("From", "Subject", "Date").
				Context(ctx).Do()
			if err != nil {
				continue
			}
			out.Messages = append(out.Messages, messageSummary{
				ID:      full.Id,
				From:    header(full, "From"),
				Subject: header(full, "Subject"),
				Date:    header(full, "Date"),
				Snippet: full.Snippet,
			})
		}
		return out
	}
}

// ---- get ----

type getInput struct {
	ID string `json:"id"`
}

type getResult struct {
	ID      string `json:"id"`
	From    string `json:"from"`
	Subject string `json:"subject"`
	Date    string `json:"date"`
	Body    string `json:"body"`
	Status  string `json:"status"`
	Error   string `json:"error,omitempty"`
}

func getMessage(svc *gmail.Service) functiontool.Func[getInput, getResult] {
	return func(ctx tool.Context, in getInput) getResult {
		full, err := svc.Users.Messages.Get(gmailUser, in.ID).Format("full").Context(ctx).Do()
		if err != nil {
			return getResult{Status: "error", Error: err.Error()}
		}
		body := extractBody(full.Payload)
		if len(body) > 4000 {
			body = body[:4000]
		}
		return getResult{
			ID:      full.Id,
			From:    header(full, "From"),
			Subject: header(full, "Subject"),
			Date:    header(full, "Date"),
			Body:    body,
			Status:  "success",
		}
	}
}

// ---- ensure label ----

type ensureLabelInput struct {
	Name string `json:"name"`
}

type ensureLabelResult struct {
	LabelID string `json:"labelId"`
	Name    string `json:"name"`
	Status  string `json:"status"`
	Error   string `json:"error,omitempty"`
}

func ensureLabel(svc *gmail.Service, mode config.ActionMode) functiontool.Func[ensureLabelInput, ensureLabelResult] {
	return func(ctx tool.Context, in ensureLabelInput) ensureLabelResult {
		id, err := resolveLabelID(ctx, svc, in.Name)
		if err != nil {
			return ensureLabelResult{Status: "error", Error: err.Error()}
		}
		if id != "" {
			return ensureLabelResult{LabelID: id, Name: in.Name, Status: "exists"}
		}
		if mode == config.ModeDryRun {
			return ensureLabelResult{Name: in.Name, Status: "dry_run_would_create"}
		}
		created, err := svc.Users.Labels.Create(gmailUser, &gmail.Label{
			Name:                  in.Name,
			LabelListVisibility:   "labelShow",
			MessageListVisibility: "show",
		}).Context(ctx).Do()
		if err != nil {
			return ensureLabelResult{Status: "error", Error: err.Error()}
		}
		return ensureLabelResult{LabelID: created.Id, Name: in.Name, Status: "created"}
	}
}

// ---- apply label ----

type applyLabelInput struct {
	MessageID       string `json:"messageId"`
	LabelName       string `json:"labelName"`
	RemoveFromInbox bool   `json:"removeFromInbox,omitempty"`
}

type applyLabelResult struct {
	Status string `json:"status"`
	Error  string `json:"error,omitempty"`
}

func applyLabel(svc *gmail.Service, mode config.ActionMode) functiontool.Func[applyLabelInput, applyLabelResult] {
	return func(ctx tool.Context, in applyLabelInput) applyLabelResult {
		if mode == config.ModeDryRun {
			return applyLabelResult{Status: fmt.Sprintf("dry_run_would_apply_%s_to_%s", in.LabelName, in.MessageID)}
		}
		labelID, err := resolveLabelID(ctx, svc, in.LabelName)
		if err != nil {
			return applyLabelResult{Status: "error", Error: err.Error()}
		}
		if labelID == "" {
			return applyLabelResult{Status: "error", Error: "label does not exist; call gmail_ensure_label first"}
		}
		req := &gmail.ModifyMessageRequest{AddLabelIds: []string{labelID}}
		if in.RemoveFromInbox {
			req.RemoveLabelIds = []string{"INBOX"}
		}
		if _, err := svc.Users.Messages.Modify(gmailUser, in.MessageID, req).Context(ctx).Do(); err != nil {
			return applyLabelResult{Status: "error", Error: err.Error()}
		}
		return applyLabelResult{Status: "applied"}
	}
}

// ---- trash ----

type trashInput struct {
	MessageID string `json:"messageId"`
}

type trashResult struct {
	Status string `json:"status"`
	Error  string `json:"error,omitempty"`
}

func trashMessage(svc *gmail.Service, mode config.ActionMode) functiontool.Func[trashInput, trashResult] {
	return func(ctx tool.Context, in trashInput) trashResult {
		if mode != config.ModeAutoTrash {
			return trashResult{Status: "disabled"}
		}
		if _, err := svc.Users.Messages.Trash(gmailUser, in.MessageID).Context(ctx).Do(); err != nil {
			return trashResult{Status: "error", Error: err.Error()}
		}
		return trashResult{Status: "trashed"}
	}
}

// ---- helpers ----

func resolveLabelID(ctx tool.Context, svc *gmail.Service, name string) (string, error) {
	resp, err := svc.Users.Labels.List(gmailUser).Context(ctx).Do()
	if err != nil {
		return "", err
	}
	for _, l := range resp.Labels {
		if strings.EqualFold(l.Name, name) {
			return l.Id, nil
		}
	}
	return "", nil
}

func header(m *gmail.Message, key string) string {
	if m.Payload == nil {
		return ""
	}
	for _, h := range m.Payload.Headers {
		if strings.EqualFold(h.Name, key) {
			return h.Value
		}
	}
	return ""
}

// extractBody walks the MIME tree and returns the first text/plain part it
// finds, falling back to text/html stripped of tags-ish, then the snippet.
func extractBody(p *gmail.MessagePart) string {
	if p == nil {
		return ""
	}
	if p.MimeType == "text/plain" && p.Body != nil && p.Body.Data != "" {
		return decodeB64URL(p.Body.Data)
	}
	for _, part := range p.Parts {
		if b := extractBody(part); b != "" {
			return b
		}
	}
	if p.MimeType == "text/html" && p.Body != nil && p.Body.Data != "" {
		return decodeB64URL(p.Body.Data)
	}
	return ""
}

// decodeB64URL decodes Gmail's URL-safe base64 body payloads.
func decodeB64URL(data string) string {
	b, err := base64.URLEncoding.DecodeString(data)
	if err != nil {
		// Gmail occasionally omits padding; retry with the raw decoder.
		if b, err = base64.RawURLEncoding.DecodeString(data); err != nil {
			return ""
		}
	}
	return string(b)
}
