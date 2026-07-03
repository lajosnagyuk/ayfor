package export

import (
	"strings"

	"github.com/lajosnagyuk/ayfor/internal/page"
)

// Markdown renders the live pages as plain Markdown. Pages are separated
// by a horizontal rule. Styling per the export policy: bold for
// double-strike, strikethrough for x-ed out cells.
func Markdown(d *page.Doc) string {
	var sb strings.Builder
	pages := d.LivePages()
	for pi, p := range pages {
		if pi > 0 {
			sb.WriteString("\n---\n\n")
		}
		prevY := -1
		for _, line := range Lines(p) {
			for i := 0; i < blankLinesBetween(prevY, line.YHalf, d); i++ {
				sb.WriteByte('\n')
			}
			prevY = line.YHalf
			sb.WriteString(renderMDLine(line.Cells))
			sb.WriteByte('\n')
		}
	}
	return sb.String()
}

func renderMDLine(cells []StyledCell) string {
	// Trim trailing spaces.
	end := len(cells)
	for end > 0 && cells[end-1].Rune == ' ' {
		end--
	}
	cells = cells[:end]

	var sb strings.Builder
	i := 0
	for i < len(cells) {
		style := cells[i].Style
		j := i
		for j < len(cells) && sameRun(cells[j], style) {
			j++
		}
		run := runString(cells[i:j])
		switch style {
		case Bold:
			body := strings.TrimRight(run, " ")
			sb.WriteString("**" + escapeMD(body) + "**")
			if trimmed := len(run) - len(body); trimmed > 0 {
				sb.WriteString(strings.Repeat(" ", trimmed))
			}
		case Struck:
			body := strings.TrimRight(run, " ")
			sb.WriteString("~~" + escapeMD(body) + "~~")
			if trimmed := len(run) - len(body); trimmed > 0 {
				sb.WriteString(strings.Repeat(" ", trimmed))
			}
		default:
			sb.WriteString(escapeMD(run))
		}
		i = j
	}
	return escapeLeadingBlockMarker(sb.String())
}

// escapeLeadingBlockMarker backslash-escapes a line-leading Markdown block
// construct (list bullet, blockquote, ordered-list number, table pipe) that
// escapeMD does not cover, so a typed line like "- item" or "1. first" is not
// reinterpreted as a list or quote.
func escapeLeadingBlockMarker(s string) string {
	i := 0
	for i < len(s) && s[i] == ' ' {
		i++
	}
	rest := s[i:]
	if strings.HasPrefix(rest, "- ") || strings.HasPrefix(rest, "+ ") ||
		strings.HasPrefix(rest, "> ") || (len(rest) > 0 && rest[0] == '|') {
		return s[:i] + `\` + rest
	}
	// Ordered list: one-or-more digits then '.' or ')' then a space.
	d := 0
	for d < len(rest) && rest[d] >= '0' && rest[d] <= '9' {
		d++
	}
	if d > 0 && d+1 < len(rest) && (rest[d] == '.' || rest[d] == ')') && rest[d+1] == ' ' {
		return s[:i] + rest[:d] + `\` + rest[d:]
	}
	return s
}

// sameRun groups a space into whatever run it is inside so single spaces
// between two bold words do not split the span.
func sameRun(c StyledCell, style CellStyle) bool {
	return c.Style == style || c.Rune == ' '
}

func runString(cells []StyledCell) string {
	var sb strings.Builder
	for _, c := range cells {
		sb.WriteRune(c.Rune)
	}
	return sb.String()
}

// mdReplacer escapes characters Markdown would otherwise interpret. Built
// once: escapeMD runs per text run (multiple times per line), and
// strings.NewReplacer builds an internal lookup table on every call.
// < and > matter as much as the rest: most Markdown renderers pass raw
// HTML through, so an unescaped typed "<b>" or "<script>" would change
// (or script) the rendered export instead of showing what was struck.
var mdReplacer = strings.NewReplacer(
	`\`, `\\`, `*`, `\*`, `_`, `\_`, `#`, `\#`,
	"`", "\\`", `~`, `\~`, `[`, `\[`, `]`, `\]`,
	`<`, `\<`, `>`, `\>`,
)

func escapeMD(s string) string {
	return mdReplacer.Replace(s)
}
