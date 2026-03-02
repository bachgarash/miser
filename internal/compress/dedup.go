package compress

import (
	"crypto/sha256"
	"fmt"
)

// deduplicateContent replaces messages whose content is identical to an
// earlier message with a short placeholder. Only messages at least
// minBlockSize bytes are considered for deduplication.
func deduplicateContent(msgs []Message, minBlockSize int) []Message {
	type entry struct {
		index int
		role  string
	}

	seen := make(map[[32]byte]entry)
	out := make([]Message, len(msgs))
	copy(out, msgs)

	for i, m := range out {
		if len(m.Content) < minBlockSize {
			continue
		}

		hash := sha256.Sum256([]byte(m.Content))
		if first, ok := seen[hash]; ok {
			out[i].Content = fmt.Sprintf(
				"[Content identical to message #%d (role: %s), %d chars omitted]",
				first.index, first.role, len(m.Content),
			)
		} else {
			seen[hash] = entry{index: m.Index, role: m.Role}
		}
	}

	return out
}
