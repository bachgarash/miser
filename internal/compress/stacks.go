package compress

import (
	"crypto/sha256"
	"fmt"
	"regexp"
	"strings"
)

// maxStackDetectSize skips stack detection for messages larger than this
// to avoid expensive regex scans on huge payloads.
const maxStackDetectSize = 512 * 1024

// Compiled regex patterns for detecting stack traces in various languages.
var (
	// Go: "goroutine N [status]:\n" followed by func/file pairs.
	goStackRe = regexp.MustCompile(`(?m)goroutine \d+ \[.*\]:\n(?:.*\n\t.*\n)+`)

	// Python: "Traceback (most recent call last):" block ending at the
	// exception line (a non-indented, non-empty line after the traceback).
	pythonStackRe = regexp.MustCompile(`(?m)Traceback \(most recent call last\):\n(?:[ \t]+.*\n)+\S[^\n]*`)

	// Node.js: "Error: ...\n    at ..." block.
	nodeStackRe = regexp.MustCompile(`(?m)(?:Error|TypeError|ReferenceError|RangeError|SyntaxError)[^\n]*\n(?:\s+at [^\n]+\n?)+`)

	// Java: "Exception: ...\n    at ..." block.
	javaStackRe = regexp.MustCompile(`(?m)(?:\w+\.)*\w+(?:Exception|Error)[^\n]*\n(?:\s+at [^\n]+\n?)+`)

	stackPatterns = []*regexp.Regexp{goStackRe, pythonStackRe, nodeStackRe, javaStackRe}
)

// normalizeRe strips numbers, hex addresses, and goroutine IDs to produce
// a canonical form for hashing. Function names and file paths survive.
var normalizeRe = regexp.MustCompile(`0x[0-9a-fA-F]+|\b\d+\b`)

// truncateStacks detects stack traces and replaces duplicate stacks with
// a short placeholder. The first occurrence of each unique stack (by
// normalised hash) is kept in full.
func truncateStacks(s string) string {
	if len(s) > maxStackDetectSize {
		// Fast pre-check: if none of the trigger words are present, skip.
		if !strings.Contains(s, "goroutine") &&
			!strings.Contains(s, "Traceback") &&
			!strings.Contains(s, "at ") {
			return s
		}
		// Too large for regex — return unmodified.
		return s
	}

	seen := make(map[[32]byte]int) // hash → count of occurrences

	for _, re := range stackPatterns {
		s = re.ReplaceAllStringFunc(s, func(match string) string {
			norm := normalizeRe.ReplaceAllString(match, "")
			hash := sha256.Sum256([]byte(norm))

			seen[hash]++
			if seen[hash] == 1 {
				return match // first occurrence: keep in full
			}

			// Count frames for the summary.
			frames := 0
			for _, line := range strings.Split(match, "\n") {
				trimmed := strings.TrimSpace(line)
				if strings.HasPrefix(trimmed, "at ") || strings.HasPrefix(trimmed, "File ") || strings.Contains(trimmed, ".go:") {
					frames++
				}
			}
			if frames == 0 {
				frames = strings.Count(match, "\n")
			}
			return fmt.Sprintf("[... %d similar stack frames omitted]", frames)
		})
	}

	return s
}
