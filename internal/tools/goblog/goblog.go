// Package goblogtools exposes tools for the Go blog (https://go.dev/blog/):
// fetching a single post's title and plain-text content, and listing recent
// posts from the blog's Atom feed. Neither tool summarizes or translates
// anything itself: the calling agent (an LLM) reads the returned data and
// produces the summary/translation in its own reply, the same way the gmail
// tools return raw message bodies for the agent to classify.
package goblogtools

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"

	"golang.org/x/net/html"
	"google.golang.org/adk/tool"
	"google.golang.org/adk/tool/functiontool"
)

// allowedHost restricts fetches to the official Go blog, so this read-only
// tool can't be used as an open URL fetcher (SSRF) via agent/user input.
const allowedHost = "go.dev"

// maxContentLen bounds how much article text is returned to the agent.
const maxContentLen = 40000

// Tools returns the go blog tools: fetching a single post by URL, and
// listing recent posts from the blog's Atom feed.
func Tools() ([]tool.Tool, error) {
	fetchTool, err := functiontool.New(functiontool.Config{
		Name:        "goblog_fetch_post",
		Description: "Fetch a Go blog post from https://go.dev/blog/... and return its title and plain-text content, for the caller to summarize and/or translate. Read-only; only go.dev URLs are allowed.",
	}, fetchPost())
	if err != nil {
		return nil, err
	}
	listTool, err := functiontool.New(functiontool.Config{
		Name:        "goblog_list_posts",
		Description: "List recent Go blog posts (title, URL, published date) from the go.dev blog's Atom feed, for use when the caller wants recent posts but hasn't given a specific URL. Read-only; only go.dev is accessed.",
	}, listPosts())
	if err != nil {
		return nil, err
	}
	return []tool.Tool{fetchTool, listTool}, nil
}

type fetchInput struct {
	URL string `json:"url"`
}

type fetchResult struct {
	Title   string `json:"title"`
	Content string `json:"content"`
	Status  string `json:"status"`
	Error   string `json:"error,omitempty"`
}

func fetchPost() functiontool.Func[fetchInput, fetchResult] {
	return func(ctx tool.Context, in fetchInput) fetchResult {
		title, content, err := fetch(ctx, in.URL)
		if err != nil {
			return fetchResult{Status: "error", Error: err.Error()}
		}
		return fetchResult{Title: title, Content: content, Status: "success"}
	}
}

// fetch downloads a go.dev page and extracts its title and article text. It
// takes a plain context.Context (tool.Context satisfies it) so it can be
// exercised directly in tests without constructing an ADK tool.Context.
func fetch(ctx context.Context, rawURL string) (title, content string, err error) {
	body, err := httpGet(ctx, rawURL)
	if err != nil {
		return "", "", err
	}
	defer body.Close()
	return parseArticle(body)
}

// httpGet validates that rawURL points at allowedHost and returns the
// response body of a GET request. Shared by fetch (post pages) and
// fetchFeed (the blog index feed) so the go.dev host restriction is
// enforced in exactly one place.
func httpGet(ctx context.Context, rawURL string) (io.ReadCloser, error) {
	u, err := url.Parse(rawURL)
	if err != nil || (u.Scheme != "https" && u.Scheme != "http") || u.Hostname() != allowedHost {
		return nil, fmt.Errorf("url must be an https://%s/... link", allowedHost)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return nil, err
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		resp.Body.Close()
		return nil, fmt.Errorf("fetch %s: unexpected status %d", u, resp.StatusCode)
	}
	return resp.Body, nil
}

// parseArticle extracts the title and main article text out of an HTML
// document, skipping boilerplate (script/style/nav/header/footer). Split out
// from fetch so the extraction logic can be tested against a fixture without
// making a real HTTP request.
func parseArticle(r io.Reader) (title, content string, err error) {
	doc, err := html.Parse(r)
	if err != nil {
		return "", "", fmt.Errorf("parse html: %w", err)
	}

	title = strings.TrimSpace(extractText(findFirst(doc, "title")))
	article := findFirst(doc, "article")
	if article == nil {
		article = findFirst(doc, "body")
	}
	content = collapseWhitespace(extractText(article))
	if len(content) > maxContentLen {
		content = content[:maxContentLen]
	}
	if content == "" {
		return "", "", fmt.Errorf("no article content found on page")
	}

	return title, content, nil
}

// findFirst returns the first node with the given tag name, depth-first.
func findFirst(n *html.Node, tag string) *html.Node {
	if n == nil {
		return nil
	}
	if n.Type == html.ElementNode && n.Data == tag {
		return n
	}
	for c := n.FirstChild; c != nil; c = c.NextSibling {
		if found := findFirst(c, tag); found != nil {
			return found
		}
	}
	return nil
}

// skipTags are elements whose text content is boilerplate/non-content and
// should not be included in the extracted article text.
var skipTags = map[string]bool{
	"script": true, "style": true, "nav": true, "header": true, "footer": true,
}

// extractText walks n's subtree, concatenating text node data while skipping
// script/style/nav/header/footer elements entirely.
func extractText(n *html.Node) string {
	if n == nil {
		return ""
	}
	var sb strings.Builder
	var walk func(*html.Node)
	walk = func(n *html.Node) {
		if n.Type == html.ElementNode && skipTags[n.Data] {
			return
		}
		if n.Type == html.TextNode {
			sb.WriteString(n.Data)
			sb.WriteString(" ")
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			walk(c)
		}
	}
	walk(n)
	return sb.String()
}

func collapseWhitespace(s string) string {
	return strings.Join(strings.Fields(s), " ")
}
