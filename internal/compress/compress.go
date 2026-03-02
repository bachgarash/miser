package compress

// Config controls which compression layers are enabled.
type Config struct {
	Whitespace      bool
	StackTruncation bool
	Deduplication   bool
	MinBlockSize    int // default 256
}

// Stats reports byte savings from compression.
type Stats struct {
	OriginalBytes   int
	CompressedBytes int
}

// Message represents a single message in a conversation.
type Message struct {
	Index   int
	Role    string
	Content string
}

// Compress runs the enabled compression layers in order:
// whitespace → stacks → deduplication. It returns the compressed
// messages and byte-level stats. If no layers are enabled the
// messages are returned unchanged.
func Compress(cfg Config, msgs []Message) ([]Message, Stats) {
	original := 0
	for _, m := range msgs {
		original += len(m.Content)
	}

	out := make([]Message, len(msgs))
	copy(out, msgs)

	if cfg.Whitespace {
		for i := range out {
			out[i].Content = normalizeWhitespace(out[i].Content)
		}
	}

	if cfg.StackTruncation {
		for i := range out {
			out[i].Content = truncateStacks(out[i].Content)
		}
	}

	if cfg.Deduplication {
		minSize := cfg.MinBlockSize
		if minSize <= 0 {
			minSize = 256
		}
		out = deduplicateContent(out, minSize)
	}

	compressed := 0
	for _, m := range out {
		compressed += len(m.Content)
	}

	return out, Stats{
		OriginalBytes:   original,
		CompressedBytes: compressed,
	}
}
