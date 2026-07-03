package export

import (
	"strings"
	"testing"
)

func TestMarkdownPreservesBlankLines(t *testing.T) {
	d := docFromText(t, "Chapter One\n\nIt was a dark and stormy night.")
	md := Markdown(d)
	if !strings.Contains(md, "Chapter One\n\nIt was") {
		t.Fatalf("blank line lost:\n%q", md)
	}
}

func TestMarkdownPreservesMultipleBlankLines(t *testing.T) {
	d := docFromText(t, "top\n\n\n\nbottom")
	md := Markdown(d)
	if !strings.Contains(md, "top\n\n\n\nbottom") {
		t.Fatalf("blank run lost:\n%q", md)
	}
}
