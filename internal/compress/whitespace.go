package compress

import (
	"strings"
)

// normalizeWhitespace trims trailing whitespace from each line and
// collapses runs of 3+ consecutive blank lines down to 2. Leading
// indentation is preserved (critical for code blocks).
func normalizeWhitespace(s string) string {
	if s == "" {
		return s
	}

	lines := strings.Split(s, "\n")
	var b strings.Builder
	b.Grow(len(s))

	blankRun := 0
	for i, line := range lines {
		trimmed := strings.TrimRight(line, " \t")

		if trimmed == "" {
			blankRun++
		} else {
			blankRun = 0
		}

		// Collapse 3+ consecutive blank lines to 2.
		if blankRun > 2 {
			continue
		}

		if i > 0 {
			b.WriteByte('\n')
		}
		b.WriteString(trimmed)
	}

	return b.String()
}
