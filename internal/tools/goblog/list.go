package goblogtools

import (
	"context"
	"encoding/xml"
	"io"

	"google.golang.org/adk/v2/agent"
	"google.golang.org/adk/v2/tool/functiontool"
)

// feedURL is the go.dev blog's Atom feed. It is not derived from any
// caller-supplied input, so listing posts can never be used to reach a host
// other than go.dev.
const feedURL = "https://go.dev/blog/" + "feed.atom"

// defaultMaxResults/maxMaxResults bound how many posts goblog_list_posts
// returns, mirroring the clamp pattern used by gmail_list_messages.
const (
	defaultMaxResults = 5
	maxMaxResults     = 20
)

type postSummary struct {
	Title     string `json:"title"`
	URL       string `json:"url"`
	Published string `json:"published"`
}

type listPostsInput struct {
	MaxResults int64 `json:"maxResults,omitempty"`
}

type listPostsResult struct {
	Posts  []postSummary `json:"posts,omitempty"`
	Status string        `json:"status"`
	Error  string        `json:"error,omitempty"`
}

func listPosts() functiontool.Func[listPostsInput, listPostsResult] {
	return func(ctx agent.Context, in listPostsInput) (listPostsResult, error) {
		posts, err := fetchFeed(ctx)
		if err != nil {
			return listPostsResult{Status: "error", Error: err.Error()}, nil
		}
		max := clampMaxResults(in.MaxResults)
		if int64(len(posts)) > max {
			posts = posts[:max]
		}
		return listPostsResult{Posts: posts, Status: "success"}, nil
	}
}

// clampMaxResults applies the same default/cap policy as gmail_list_messages:
// non-positive falls back to a sane default, and anything above the cap is
// truncated so the agent can't be asked to summarize an unbounded feed.
func clampMaxResults(n int64) int64 {
	if n <= 0 {
		return defaultMaxResults
	}
	if n > maxMaxResults {
		return maxMaxResults
	}
	return n
}

// fetchFeed downloads and parses the go.dev blog's Atom feed. Split out from
// parseFeed so the parsing logic can be tested against a fixture without a
// real HTTP request, the same way fetch/parseArticle are split.
func fetchFeed(ctx context.Context) ([]postSummary, error) {
	body, err := httpGet(ctx, feedURL)
	if err != nil {
		return nil, err
	}
	defer func() { _ = body.Close() }()
	return parseFeed(body)
}

type atomFeed struct {
	Entries []atomEntry `xml:"entry"`
}

type atomEntry struct {
	Title     string     `xml:"title"`
	Links     []atomLink `xml:"link"`
	Published string     `xml:"published"`
	Updated   string     `xml:"updated"`
}

type atomLink struct {
	Rel  string `xml:"rel,attr"`
	Href string `xml:"href,attr"`
}

// parseFeed extracts post summaries from an Atom feed document, newest
// first (the feed's own entry order). Entries without a usable link are
// skipped rather than failing the whole feed.
func parseFeed(r io.Reader) ([]postSummary, error) {
	var feed atomFeed
	if err := xml.NewDecoder(r).Decode(&feed); err != nil {
		return nil, err
	}

	posts := make([]postSummary, 0, len(feed.Entries))
	for _, e := range feed.Entries {
		href := entryLink(e)
		if href == "" {
			continue
		}
		published := e.Published
		if published == "" {
			published = e.Updated
		}
		posts = append(posts, postSummary{
			Title:     e.Title,
			URL:       href,
			Published: published,
		})
	}
	return posts, nil
}

// entryLink picks the alternate link out of an Atom entry (falling back to
// the first link present), returning "" if the entry has no link at all.
func entryLink(e atomEntry) string {
	for _, l := range e.Links {
		if l.Rel == "alternate" || l.Rel == "" {
			return l.Href
		}
	}
	if len(e.Links) > 0 {
		return e.Links[0].Href
	}
	return ""
}
