package goblogtools

import (
	"context"
	"strings"
	"testing"
)

const fixtureHTML = `<!doctype html>
<html>
<head><title>Slices: usage and internals - The Go Programming Language</title>
<style>.hidden{display:none}</style>
</head>
<body>
<nav>Home | Blog | Docs</nav>
<header>Go Blog Header</header>
<article>
  <h1>Slices: usage and internals</h1>
  <p>Go's slices provide a convenient and efficient means of working with
  sequences of typed data.</p>
  <script>console.log("should be skipped")</script>
  <p>Slices are analogous to arrays in other languages, but have some
  unusual properties.</p>
</article>
<footer>Copyright Go contributors</footer>
</body>
</html>`

func TestParseArticleExtractsTitleAndBodySkipsBoilerplate(t *testing.T) {
	title, content, err := parseArticle(strings.NewReader(fixtureHTML))
	if err != nil {
		t.Fatalf("parseArticle failed: %v", err)
	}
	if !strings.Contains(title, "Slices: usage and internals") {
		t.Errorf("title = %q, want to contain %q", title, "Slices: usage and internals")
	}
	if !strings.Contains(content, "convenient and efficient") {
		t.Errorf("content missing expected article text: %q", content)
	}
	if !strings.Contains(content, "unusual properties") {
		t.Errorf("content missing second paragraph: %q", content)
	}
	if strings.Contains(content, "should be skipped") {
		t.Errorf("content leaked <script> text: %q", content)
	}
	if strings.Contains(content, "Go Blog Header") || strings.Contains(content, "Copyright Go contributors") {
		t.Errorf("content leaked header/footer boilerplate: %q", content)
	}
}

func TestFetchRejectsNonGoDevHost(t *testing.T) {
	if _, _, err := fetch(context.Background(), "https://evil.example.com/blog/slices"); err == nil {
		t.Fatalf("expected error for non-go.dev host")
	}
	if _, _, err := fetch(context.Background(), "ftp://go.dev/blog/slices"); err == nil {
		t.Fatalf("expected error for non-http(s) scheme")
	}
}
