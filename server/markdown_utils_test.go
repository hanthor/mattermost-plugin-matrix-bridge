package main

import (
	"strings"
	"testing"
)

func TestConvertMarkdownToHTML(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		contains []string
		excludes []string
	}{
		{"bold", "This is **bold** text", []string{"<strong>bold</strong>"}, nil},
		{"bold underscores", "This is __bold__ text", []string{"<strong>bold</strong>"}, nil},
		{"italic", "This is *italic* text", []string{"<em>italic</em>"}, nil},
		{"italic underscores", "This is _italic_ text", []string{"<em>italic</em>"}, nil},
		{"strikethrough", "This is ~~gone~~ text", []string{"<del>gone</del>"}, nil},
		{"inline code", "This is `code` text", []string{"<code>code</code>"}, nil},
		{"code block", "```\ncode block\n```", []string{"<pre><code>code block\n</code></pre>"}, nil},
		{"fenced language", "```javascript\nconsole.log('hello');\n```", []string{`<code class="language-javascript">`}, nil},
		{"link", "Check out [this link](https://example.com)", []string{`<a href="https://example.com">this link</a>`}, nil},
		{"bare url is linked", "see https://example.com/a_b today", []string{`<a href="https://example.com/a_b">`}, nil},
		{"single newline is a line break", "Line 1\nLine 2", []string{"Line 1<br>\nLine 2"}, nil},
		{"paragraphs", "Paragraph 1\n\nParagraph 2", []string{"<p>Paragraph 1</p>", "<p>Paragraph 2</p>"}, nil},
		{"bullet list", "- one\n- two", []string{"<ul>", "<li>one</li>", "<li>two</li>"}, nil},
		{"numbered list", "1. one\n2. two", []string{"<ol>", "<li>one</li>"}, nil},
		{"blockquote", "> quoted", []string{"<blockquote>", "quoted"}, nil},
		{"heading", "# Main Heading", []string{"<h1>Main Heading</h1>"}, nil},
		{"heading without space is text", "##NoSpace", []string{"##NoSpace"}, []string{"<h2>"}},
		{"table", "| Name | Age |\n|------|-----|\n| John | 30 |", []string{"<table>", "<th>Name</th>", "<td>John</td>"}, nil},
		{"task list", "- [x] done\n- [ ] todo", []string{`type="checkbox"`}, nil},
		{"raw html is escaped", "This has <script>alert('xss')</script> tags", []string{"&lt;script&gt;"}, []string{"<script>"}},
		{"javascript link is dropped", "[x](javascript:alert(1))", nil, []string{`href="javascript:`}},
		{"empty", "   ", nil, []string{"<"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := convertMarkdownToHTML(tt.input)
			for _, want := range tt.contains {
				if !strings.Contains(got, want) {
					t.Errorf("convertMarkdownToHTML(%q) = %q, want it to contain %q", tt.input, got, want)
				}
			}
			for _, unwanted := range tt.excludes {
				if strings.Contains(got, unwanted) {
					t.Errorf("convertMarkdownToHTML(%q) = %q, must not contain %q", tt.input, got, unwanted)
				}
			}
		})
	}
}

func TestConvertMattermostToMatrix(t *testing.T) {
	plain, formatted := convertMattermostToMatrix("Just plain text")
	if plain != "Just plain text" || formatted != "" {
		t.Errorf("plain text should not get a formatted body, got %q / %q", plain, formatted)
	}

	plain, formatted = convertMattermostToMatrix("Quotes aren't \"formatting\" & neither are ampersands")
	if formatted != "" {
		t.Errorf("entities alone should not produce a formatted body, got %q", formatted)
	}
	if plain != "Quotes aren't \"formatting\" & neither are ampersands" {
		t.Errorf("plain text changed: %q", plain)
	}

	plain, formatted = convertMattermostToMatrix("This is **bold** :wave:")
	if plain != "This is **bold** 👋" {
		t.Errorf("plain body should keep markdown source with emoji converted, got %q", plain)
	}
	if !strings.Contains(formatted, "<strong>bold</strong> 👋") {
		t.Errorf("formatted body = %q", formatted)
	}

	_, formatted = convertMattermostToMatrix("- bridged in both directions\n- with your name and avatar")
	if !strings.Contains(formatted, "<ul>") || !strings.Contains(formatted, "<li>bridged in both directions</li>") {
		t.Errorf("lists should render as HTML lists, got %q", formatted)
	}

	plain, formatted = convertMattermostToMatrix("")
	if plain != "" || formatted != "" {
		t.Errorf("empty input should stay empty, got %q / %q", plain, formatted)
	}
}

func TestEscapeMarkdownForMattermost(t *testing.T) {
	cases := map[string]string{
		"plain text":                       "plain text",
		"5*3*2 = 30":                       `5\*3\*2 = 30`,
		"file_name_here and __init__":      `file\_name\_here and \_\_init\_\_`,
		"**literal** asterisks":            `\*\*literal\*\* asterisks`,
		"see https://example.com/a_b*c ok": `see https://example.com/a_b*c ok`,
		"# not a heading":                  `\# not a heading`,
		"- not a list\n> not a quote":      "\\- not a list\n\\> not a quote",
		"@carol :tada: stays":              "@carol :tada: stays",
		"a `tick` here":                    "a \\`tick\\` here",
		"":                                 "",
	}
	for in, want := range cases {
		if got := escapeMarkdownForMattermost(in); got != want {
			t.Errorf("escapeMarkdownForMattermost(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestIsValidURL(t *testing.T) {
	valid := []string{"https://example.com", "http://example.com/path", "mailto:a@b.c", "/relative/path", "matrix:r/room:example.org"}
	for _, u := range valid {
		if !isValidURL(u) {
			t.Errorf("isValidURL(%q) = false, want true", u)
		}
	}
	invalid := []string{"javascript:alert(1)", "data:text/html,x", "vbscript:x", "file:///etc/passwd", ""}
	for _, u := range invalid {
		if isValidURL(u) {
			t.Errorf("isValidURL(%q) = true, want false", u)
		}
	}
}
