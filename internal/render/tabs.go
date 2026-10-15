package render

import (
	"strings"

	"charm.land/lipgloss/v2"
)

func expandTabs(text string) string {
	if !strings.ContainsRune(text, '\t') {
		return text
	}
	var out strings.Builder
	column := 0
	for {
		before, after, found := strings.Cut(text, "\t")
		out.WriteString(before)
		column += lipgloss.Width(before)
		if !found {
			break
		}
		spaces := 8 - column%8
		out.WriteString(strings.Repeat(" ", spaces))
		column += spaces
		text = after
	}
	return out.String()
}
