package source

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"

	d "github.com/KanaDoodle/CampusTrace/internal/domain"
	"golang.org/x/net/html"
)

// HTML responses use the same pacing, retry, size bound and conditional cache
// as JSON. Providers select only the public job facts from the document.
type htmlDocument string

func decodePlatformBody(body []byte, dst any) error {
	if document, ok := dst.(*htmlDocument); ok {
		*document = htmlDocument(body)
		return nil
	}
	return json.Unmarshal(body, dst)
}
func validPlatformBody(body []byte, dst any) bool {
	if _, ok := dst.(*htmlDocument); ok {
		return len(body) > 0
	}
	return json.Valid(body)
}
func (a PublicPlatform) anonymousOrigin(s d.Source, origin string) (PublicPlatform, error) {
	if _, err := PlatformURL(s); err != nil {
		return a, err
	}
	u, _ := url.Parse(origin)
	base := a.Client
	if base == nil {
		base = publicPlatformClient
	}
	client := *base
	client.Jar = nil
	client.CheckRedirect = func(req *http.Request, via []*http.Request) error {
		if req.URL.Scheme != "https" || req.URL.Host != u.Host || len(via) > 4 {
			return fmt.Errorf("public recruiting redirect outside origin")
		}
		if base.CheckRedirect != nil {
			return base.CheckRedirect(req, via)
		}
		return nil
	}
	a.Client = &client
	return a, nil
}
func (a PublicPlatform) document(ctx context.Context, s d.Source, raw string, body any) (*html.Node, error) {
	var v htmlDocument
	method := http.MethodGet
	if body != nil {
		method = http.MethodPost
	}
	if err := a.request(ctx, s, method, raw, body, &v); err != nil {
		return nil, err
	}
	root, err := html.Parse(strings.NewReader(string(v)))
	if err != nil {
		return nil, fail("SCHEMA_INVALID", false, 200)
	}
	return root, nil
}
func nodeAttr(n *html.Node, key string) string {
	for _, attr := range n.Attr {
		if attr.Key == key {
			return attr.Val
		}
	}
	return ""
}
func nodeClass(n *html.Node, class string) bool {
	for _, item := range strings.Fields(nodeAttr(n, "class")) {
		if item == class {
			return true
		}
	}
	return false
}
func nodes(root *html.Node, match func(*html.Node) bool) []*html.Node {
	out := []*html.Node{}
	var walk func(*html.Node)
	walk = func(n *html.Node) {
		if match(n) {
			out = append(out, n)
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			walk(c)
		}
	}
	walk(root)
	return out
}
func nodeText(root *html.Node) string {
	var b strings.Builder
	var walk func(*html.Node)
	walk = func(n *html.Node) {
		if n.Type == html.ElementNode && (n.Data == "script" || n.Data == "style" || n.Data == "noscript") {
			return
		}
		if n.Type == html.TextNode {
			if text := strings.TrimSpace(n.Data); text != "" {
				b.WriteString(text)
				b.WriteByte('\n')
			}
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			walk(c)
		}
	}
	walk(root)
	return strings.TrimSpace(b.String())
}
func uniqueNode(root *html.Node, match func(*html.Node) bool) (*html.Node, error) {
	found := nodes(root, match)
	if len(found) != 1 {
		return nil, fail("SCHEMA_INVALID", false, 200)
	}
	return found[0], nil
}
