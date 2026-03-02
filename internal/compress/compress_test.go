package compress

import (
	"strings"
	"testing"
)

// ── Whitespace tests ──────────────────────────────────────────────────

func TestNormalizeWhitespace_Empty(t *testing.T) {
	if got := normalizeWhitespace(""); got != "" {
		t.Errorf("expected empty string, got %q", got)
	}
}

func TestNormalizeWhitespace_TrailingSpaces(t *testing.T) {
	input := "hello   \nworld\t\t\n"
	want := "hello\nworld\n"
	if got := normalizeWhitespace(input); got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestNormalizeWhitespace_CollapseBlankLines(t *testing.T) {
	input := "a\n\n\n\n\nb"
	want := "a\n\n\nb"
	if got := normalizeWhitespace(input); got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestNormalizeWhitespace_PreserveIndentation(t *testing.T) {
	input := "func main() {\n\tfmt.Println(\"hi\")   \n}"
	want := "func main() {\n\tfmt.Println(\"hi\")\n}"
	if got := normalizeWhitespace(input); got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestNormalizeWhitespace_TwoBlankLinesPreserved(t *testing.T) {
	input := "a\n\n\nb"
	want := "a\n\n\nb"
	if got := normalizeWhitespace(input); got != want {
		t.Errorf("two blank lines should be preserved: got %q, want %q", got, want)
	}
}

// ── Stack trace tests ─────────────────────────────────────────────────

func TestTruncateStacks_GoTrace(t *testing.T) {
	stack := "goroutine 1 [running]:\nmain.foo()\n\t/app/main.go:10\nmain.bar()\n\t/app/main.go:20\n"
	input := stack + "\nsome text\n" + stack
	got := truncateStacks(input)
	if strings.Count(got, "goroutine") != 1 {
		t.Errorf("expected one goroutine header preserved, got:\n%s", got)
	}
	if !strings.Contains(got, "similar stack frames omitted") {
		t.Error("expected omission placeholder")
	}
}

func TestTruncateStacks_PythonTrace(t *testing.T) {
	stack := "Traceback (most recent call last):\n  File \"app.py\", line 10, in main\n    foo()\n  File \"app.py\", line 20, in foo\n    bar()\nValueError: bad value"
	input := stack + "\n\n" + stack
	got := truncateStacks(input)
	if strings.Count(got, "Traceback") != 1 {
		t.Errorf("expected one Traceback preserved, got:\n%s", got)
	}
	if !strings.Contains(got, "similar stack frames omitted") {
		t.Error("expected omission placeholder")
	}
}

func TestTruncateStacks_NodeTrace(t *testing.T) {
	stack := "Error: something broke\n    at Object.<anonymous> (/app/index.js:5:3)\n    at Module._compile (node:internal/modules/cjs/loader:1376:14)\n"
	input := stack + "text between\n" + stack
	got := truncateStacks(input)
	if strings.Count(got, "Error: something broke") != 1 {
		t.Errorf("expected one Error header preserved, got:\n%s", got)
	}
}

func TestTruncateStacks_JavaTrace(t *testing.T) {
	stack := "java.lang.NullPointerException: oops\n\tat com.example.Main.run(Main.java:42)\n\tat com.example.Main.main(Main.java:10)\n"
	input := stack + "log line\n" + stack
	got := truncateStacks(input)
	if strings.Count(got, "NullPointerException") != 1 {
		t.Errorf("expected one exception preserved, got:\n%s", got)
	}
}

func TestTruncateStacks_DifferentStacksPreserved(t *testing.T) {
	stack1 := "goroutine 1 [running]:\nmain.foo()\n\t/app/main.go:10\n"
	stack2 := "goroutine 2 [running]:\nmain.baz()\n\t/app/other.go:99\n"
	input := stack1 + "\n" + stack2
	got := truncateStacks(input)
	if strings.Contains(got, "omitted") {
		t.Error("different stacks should both be preserved")
	}
}

func TestTruncateStacks_NoStacks(t *testing.T) {
	input := "just some normal text\nwith no stack traces"
	got := truncateStacks(input)
	if got != input {
		t.Errorf("text without stacks should be unchanged")
	}
}

func TestTruncateStacks_SingleStack(t *testing.T) {
	input := "goroutine 1 [running]:\nmain.foo()\n\t/app/main.go:10\n"
	got := truncateStacks(input)
	if got != input {
		t.Errorf("single stack should be unchanged")
	}
}

// ── Dedup tests ───────────────────────────────────────────────────────

func TestDedup_IdenticalLargeMessages(t *testing.T) {
	content := strings.Repeat("x", 300)
	msgs := []Message{
		{Index: 0, Role: "user", Content: content},
		{Index: 1, Role: "user", Content: content},
	}
	got := deduplicateContent(msgs, 256)
	if got[0].Content != content {
		t.Error("first message should be unchanged")
	}
	if !strings.Contains(got[1].Content, "identical to message #0") {
		t.Errorf("second message should be placeholder, got: %s", got[1].Content)
	}
}

func TestDedup_BelowMinSize(t *testing.T) {
	msgs := []Message{
		{Index: 0, Role: "user", Content: "short"},
		{Index: 1, Role: "user", Content: "short"},
	}
	got := deduplicateContent(msgs, 256)
	if got[0].Content != "short" || got[1].Content != "short" {
		t.Error("messages below min size should not be deduped")
	}
}

func TestDedup_MultipleDuplicateGroups(t *testing.T) {
	a := strings.Repeat("a", 300)
	b := strings.Repeat("b", 300)
	msgs := []Message{
		{Index: 0, Role: "user", Content: a},
		{Index: 1, Role: "assistant", Content: b},
		{Index: 2, Role: "user", Content: a},
		{Index: 3, Role: "assistant", Content: b},
	}
	got := deduplicateContent(msgs, 256)
	if got[0].Content != a {
		t.Error("first 'a' should be preserved")
	}
	if got[1].Content != b {
		t.Error("first 'b' should be preserved")
	}
	if !strings.Contains(got[2].Content, "identical to message #0") {
		t.Errorf("third msg should reference #0, got: %s", got[2].Content)
	}
	if !strings.Contains(got[3].Content, "identical to message #1") {
		t.Errorf("fourth msg should reference #1, got: %s", got[3].Content)
	}
}

// ── Integration tests ─────────────────────────────────────────────────

func TestCompress_AllEnabled(t *testing.T) {
	content := strings.Repeat("x", 300) + "\n\n\n\n\n"
	msgs := []Message{
		{Index: 0, Role: "user", Content: content},
		{Index: 1, Role: "user", Content: content},
	}
	cfg := Config{
		Whitespace:      true,
		StackTruncation: true,
		Deduplication:   true,
		MinBlockSize:    256,
	}
	got, stats := Compress(cfg, msgs)
	if stats.OriginalBytes == 0 {
		t.Error("original bytes should be > 0")
	}
	if stats.CompressedBytes >= stats.OriginalBytes {
		t.Errorf("compressed (%d) should be < original (%d)", stats.CompressedBytes, stats.OriginalBytes)
	}
	// Second message should be deduped.
	if !strings.Contains(got[1].Content, "identical") {
		t.Errorf("expected dedup placeholder, got: %s", got[1].Content)
	}
}

func TestCompress_AllDisabled(t *testing.T) {
	msgs := []Message{
		{Index: 0, Role: "user", Content: "hello   \n\n\n\n\n"},
	}
	cfg := Config{}
	got, stats := Compress(cfg, msgs)
	if got[0].Content != msgs[0].Content {
		t.Error("with all layers disabled, content should be unchanged")
	}
	if stats.OriginalBytes != stats.CompressedBytes {
		t.Error("with all layers disabled, bytes should be equal")
	}
}

func TestCompress_StatsAccuracy(t *testing.T) {
	msgs := []Message{
		{Index: 0, Role: "user", Content: "hello   "},
	}
	cfg := Config{Whitespace: true}
	_, stats := Compress(cfg, msgs)
	if stats.OriginalBytes != 8 {
		t.Errorf("original bytes: got %d, want 8", stats.OriginalBytes)
	}
	if stats.CompressedBytes != 5 {
		t.Errorf("compressed bytes: got %d, want 5", stats.CompressedBytes)
	}
}
