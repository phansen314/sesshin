package payload

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
)

// The decoder's cost (implementation-spec.md, JSON reading): the statusline's
// payload on every tick, and post-tool-use's, the hottest hook, whose size is
// mostly tool_response.

func BenchmarkStatusline(b *testing.B) { bench(b, statuslinePayload(b)) }

func BenchmarkPostToolUse1KB(b *testing.B)  { bench(b, postToolUse(b, 1<<10)) }
func BenchmarkPostToolUse50KB(b *testing.B) { bench(b, postToolUse(b, 50<<10)) }
func BenchmarkPostToolUse1MB(b *testing.B)  { bench(b, postToolUse(b, 1<<20)) }

func bench(b *testing.B, data []byte) {
	b.SetBytes(int64(len(data)))
	b.ReportAllocs()
	for b.Loop() {
		if p := Decode(bytes.NewReader(data)); p.Err != nil || p.SessionID == "" {
			b.Fatalf("%+v", p)
		}
	}
}

// postToolUse is a Read's PostToolUse payload, in Claude Code's key order,
// of about size bytes: the file's content, source code with the escapes JSON
// gives tabs, newlines, and quotes, is all but the first few hundred.
func postToolUse(tb testing.TB, size int) []byte {
	tb.Helper()
	const line = "\tif err := d.skip(); err != nil {\n\t\treturn fmt.Errorf(\"member %q: %w\", key, err)\n\t}\n"
	head := fmt.Sprintf(`{"session_id":%q,"transcript_path":"/home/me/.claude/projects/-home-me-code-sesshin/%s.jsonl",`+
		`"cwd":"/home/me/code/sesshin","permission_mode":"acceptEdits","hook_event_name":"PostToolUse","tool_name":"Read",`+
		`"tool_input":{"file_path":"/home/me/code/sesshin/internal/payload/payload.go"},"tool_response":{"type":"text","file":`+
		`{"filePath":"/home/me/code/sesshin/internal/payload/payload.go","content":`, uuid, uuid)
	tail := `,"numLines":120,"startLine":1,"totalLines":120}},"tool_use_id":"toolu_01ABCDEFGHIJKLMNOPQRSTUV","prompt_id":"5c0d6c2e-0f7e-4c0a-9a43-2d1b4f6e8a90"}`
	esc, _ := json.Marshal(line)
	n := max(1, (size-len(head)-len(tail))/(len(esc)-2))
	c, _ := json.Marshal(strings.Repeat(line, n))
	return []byte(head + string(c) + tail)
}
