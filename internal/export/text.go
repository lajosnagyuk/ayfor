package export

import (
	"strings"

	"github.com/lajosnagyuk/ayfor/internal/page"
)

// Text renders the live pages as plain text: the characters as typed, blank
// lines preserved, trailing spaces trimmed, sheets separated by a form feed
// (which import reads back as a new sheet, so text round-trips). No markup -
// double-strike and x-out carry no styling here, only their letters, which
// is the honest plain-text rendering.
func Text(d *page.Doc) string {
	var sb strings.Builder
	for pi, p := range d.LivePages() {
		if pi > 0 {
			sb.WriteByte('\f')
		}
		prevY := -1
		for _, line := range Lines(p) {
			for range blankLinesBetween(prevY, line.YHalf, d) {
				sb.WriteByte('\n')
			}
			prevY = line.YHalf
			sb.WriteString(strings.TrimRight(runString(line.Cells), " "))
			sb.WriteByte('\n')
		}
	}
	return sb.String()
}
