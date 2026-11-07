package main

import (
	"bytes"
	"html"
	"regexp"
	"strings"

	"github.com/yuin/goldmark"
	"github.com/yuin/goldmark/ast"
	"github.com/yuin/goldmark/extension"
	"github.com/yuin/goldmark/renderer"
	goldmarkhtml "github.com/yuin/goldmark/renderer/html"
	"github.com/yuin/goldmark/util"
)

// Mattermost renders CommonMark with GitHub extensions (tables, strikethrough,
// task lists, bare URLs) and treats a single newline as a line break. The same
// dialect is rendered here to the HTML subset Matrix clients display, so a
// message looks the same on both sides.
var mattermostMarkdown = goldmark.New(
	goldmark.WithExtensions(extension.GFM),
	goldmark.WithRendererOptions(
		goldmarkhtml.WithHardWraps(),
		renderer.WithNodeRenderers(util.Prioritized(&escapeRawHTMLRenderer{}, 100)),
	),
)

// escapeRawHTMLRenderer shows HTML typed into a message as text, as
// Mattermost does, instead of dropping it or passing it through.
type escapeRawHTMLRenderer struct{}

func (r *escapeRawHTMLRenderer) RegisterFuncs(reg renderer.NodeRendererFuncRegisterer) {
	reg.Register(ast.KindRawHTML, r.renderRawHTML)
	reg.Register(ast.KindHTMLBlock, r.renderHTMLBlock)
}

func (r *escapeRawHTMLRenderer) renderRawHTML(w util.BufWriter, source []byte, node ast.Node, entering bool) (ast.WalkStatus, error) {
	if !entering {
		return ast.WalkSkipChildren, nil
	}
	n := node.(*ast.RawHTML)
	for i := 0; i < n.Segments.Len(); i++ {
		segment := n.Segments.At(i)
		_, _ = w.WriteString(html.EscapeString(string(segment.Value(source))))
	}
	return ast.WalkSkipChildren, nil
}

func (r *escapeRawHTMLRenderer) renderHTMLBlock(w util.BufWriter, source []byte, node ast.Node, entering bool) (ast.WalkStatus, error) {
	n := node.(*ast.HTMLBlock)
	if entering {
		_, _ = w.WriteString("<p>")
		for i := 0; i < n.Lines().Len(); i++ {
			line := n.Lines().At(i)
			_, _ = w.WriteString(html.EscapeString(string(line.Value(source))))
		}
		return ast.WalkContinue, nil
	}
	if n.HasClosure() {
		_, _ = w.WriteString(html.EscapeString(string(n.ClosureLine.Value(source))))
	}
	_, _ = w.WriteString("</p>\n")
	return ast.WalkContinue, nil
}

// convertMarkdownToHTML renders Mattermost Markdown as Matrix HTML. Raw HTML
// in the source is escaped, never passed through.
func convertMarkdownToHTML(markdown string) string {
	if strings.TrimSpace(markdown) == "" {
		return ""
	}
	var buf bytes.Buffer
	if err := mattermostMarkdown.Convert([]byte(markdown), &buf); err != nil {
		return html.EscapeString(markdown)
	}
	return strings.TrimSpace(buf.String())
}

var singleParagraph = regexp.MustCompile(`(?s)\A<p>(.*)</p>\z`)

// convertMattermostToMatrix converts Mattermost markdown content to Matrix format
// Returns both plain text and HTML versions
func convertMattermostToMatrix(mattermostContent string) (plainText string, htmlContent string) {
	// Matrix clients do not render :shortcodes:, so emit Unicode emoji.
	mattermostContent = replaceEmojiShortcodes(mattermostContent)

	// Plain text is the original content with some cleanup
	plainText = strings.TrimSpace(mattermostContent)

	// Convert to HTML
	htmlContent = convertMarkdownToHTML(mattermostContent)

	// A message without any formatting renders as a single paragraph whose
	// text equals the source; send it as plain text only.
	if m := singleParagraph.FindStringSubmatch(htmlContent); m != nil && html.UnescapeString(m[1]) == plainText {
		htmlContent = ""
	}

	return plainText, htmlContent
}

// isValidURL reports whether a link target is safe to emit.
func isValidURL(url string) bool {
	if url == "" {
		return false
	}
	lower := strings.ToLower(url)
	for _, prefix := range []string{"http://", "https://", "mailto:", "matrix:", "mxc://"} {
		if strings.HasPrefix(lower, prefix) {
			return true
		}
	}
	for _, dangerous := range []string{"javascript:", "data:", "vbscript:", "file:"} {
		if strings.HasPrefix(lower, dangerous) {
			return false
		}
	}
	// Relative URLs without a scheme are allowed.
	return !strings.Contains(url, ":")
}

// markdownSpecial matches characters that Mattermost would interpret as
// Markdown inside otherwise plain text.
var (
	markdownSpecial   = regexp.MustCompile("[\\\\`*_~\\[\\]<>|]")
	markdownLineStart = regexp.MustCompile(`(?m)^(\s*)([#>+-]|\d+[.)])(\s)`)
	bareURL           = regexp.MustCompile(`https?://[^\s<>]+|mailto:[^\s<>]+`)
)

// escapeMarkdownForMattermost makes plain text display verbatim in Mattermost.
// Matrix bodies without a formatted_body are plain text by specification, so
// "5*3*2" or "snake_case_*" must not become emphasis. URLs are left intact so
// Mattermost can still link them, and @mentions and :emoji: keep working.
func escapeMarkdownForMattermost(text string) string {
	if text == "" || !markdownSpecial.MatchString(text) && !markdownLineStart.MatchString(text) {
		return text
	}
	var out strings.Builder
	last := 0
	for _, loc := range bareURL.FindAllStringIndex(text, -1) {
		out.WriteString(escapeMarkdownSegment(text[last:loc[0]]))
		out.WriteString(text[loc[0]:loc[1]])
		last = loc[1]
	}
	out.WriteString(escapeMarkdownSegment(text[last:]))
	return out.String()
}

func escapeMarkdownSegment(segment string) string {
	segment = markdownSpecial.ReplaceAllString(segment, `\$0`)
	return markdownLineStart.ReplaceAllString(segment, `$1\$2$3`)
}
