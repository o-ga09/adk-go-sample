package goblogtools

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/google/jsonschema-go/jsonschema"
)

// mustValidate replays the validation the ADK's functiontool runs on every
// tool call/result: infer a schema from the struct, then validate the JSON
// payload against it. Mirrors the helper of the same name in
// internal/tools/gmail/gmail_test.go (see .claude/rules/tool-json-schema.md).
func mustValidate[T any](t *testing.T, payload string) {
	t.Helper()
	schema, err := jsonschema.For[T](nil)
	if err != nil {
		t.Fatalf("infer schema: %v", err)
	}
	resolved, err := schema.Resolve(nil)
	if err != nil {
		t.Fatalf("resolve schema: %v", err)
	}
	var m map[string]any
	if err := json.Unmarshal([]byte(payload), &m); err != nil {
		t.Fatalf("unmarshal payload: %v", err)
	}
	if err := resolved.Validate(m); err != nil {
		t.Errorf("payload %s rejected by inferred schema: %v", payload, err)
	}
}

const fixtureAtomFeed = `<?xml version="1.0" encoding="utf-8"?>
<feed xmlns="http://www.w3.org/2005/Atom">
  <title>The Go Programming Language Blog</title>
  <id>tag:go.dev,2013:go.dev/blog</id>
  <updated>2026-07-10T00:00:00-00:00</updated>
  <entry>
    <title>Deploying Go servers with confidence</title>
    <link rel="alternate" href="https://go.dev/blog/deploy-with-confidence"/>
    <id>tag:go.dev,2013:go.dev/blog/deploy-with-confidence</id>
    <published>2026-07-10T00:00:00-00:00</published>
    <updated>2026-07-10T00:00:00-00:00</updated>
    <summary>How to deploy Go servers reliably.</summary>
  </entry>
  <entry>
    <title>Slices: usage and internals</title>
    <link rel="alternate" href="https://go.dev/blog/slices"/>
    <id>tag:go.dev,2013:go.dev/blog/slices</id>
    <published>2026-06-01T00:00:00-00:00</published>
    <updated>2026-06-01T00:00:00-00:00</updated>
    <summary>Slices in Go.</summary>
  </entry>
  <entry>
    <title>Missing link entry, should be skipped</title>
    <id>tag:go.dev,2013:go.dev/blog/no-link</id>
    <published>2026-05-01T00:00:00-00:00</published>
  </entry>
</feed>`

func TestParseFeedExtractsTitleURLPublished(t *testing.T) {
	posts, err := parseFeed(strings.NewReader(fixtureAtomFeed))
	if err != nil {
		t.Fatalf("parseFeed failed: %v", err)
	}
	if len(posts) != 2 {
		t.Fatalf("len(posts) = %d, want 2 (entry without a link must be skipped)", len(posts))
	}
	if posts[0].Title != "Deploying Go servers with confidence" {
		t.Errorf("posts[0].Title = %q", posts[0].Title)
	}
	if posts[0].URL != "https://go.dev/blog/deploy-with-confidence" {
		t.Errorf("posts[0].URL = %q", posts[0].URL)
	}
	if posts[0].Published != "2026-07-10T00:00:00-00:00" {
		t.Errorf("posts[0].Published = %q", posts[0].Published)
	}
	if posts[1].Title != "Slices: usage and internals" {
		t.Errorf("posts[1].Title = %q", posts[1].Title)
	}
}

func TestParseFeedRejectsInvalidXML(t *testing.T) {
	if _, err := parseFeed(strings.NewReader("not xml")); err == nil {
		t.Fatal("expected error for invalid XML")
	}
}

func TestClampMaxResultsDefaultsAndCaps(t *testing.T) {
	tests := []struct {
		name  string
		input int64
		want  int64
	}{
		{name: "zero uses default", input: 0, want: defaultMaxResults},
		{name: "negative uses default", input: -1, want: defaultMaxResults},
		{name: "within range is unchanged", input: 3, want: 3},
		{name: "above cap is clamped", input: 100, want: maxMaxResults},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := clampMaxResults(tt.input); got != tt.want {
				t.Errorf("clampMaxResults(%d) = %d, want %d", tt.input, got, tt.want)
			}
		})
	}
}

func TestListPostsInputSchemaAllowsOmittedMaxResults(t *testing.T) {
	mustValidate[listPostsInput](t, `{}`)
}

func TestListPostsResultSchemaAllowsZeroPosts(t *testing.T) {
	raw, err := json.Marshal(listPostsResult{Status: "success"})
	if err != nil {
		t.Fatal(err)
	}
	mustValidate[listPostsResult](t, string(raw))
}

func TestListPostsResultSchemaAllowsErrorResult(t *testing.T) {
	raw, err := json.Marshal(listPostsResult{Status: "error", Error: "fetch failed"})
	if err != nil {
		t.Fatal(err)
	}
	mustValidate[listPostsResult](t, string(raw))
}
