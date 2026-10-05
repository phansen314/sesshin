package model

import (
	"testing"

	"github.com/phansen314/sesshin/internal/jsonio"
)

// Validation runs on every hook (implementation-spec.md, JSON reading): each
// file's parse alone, and its whole read, parse and validation.

func BenchmarkParseLifecycle(b *testing.B) { benchParse(b, "lifecycle.json") }
func BenchmarkReadLifecycle(b *testing.B) {
	data := []byte(fixture(b, "lifecycle.json"))
	b.SetBytes(int64(len(data)))
	for b.Loop() {
		if _, r := ReadLifecycle(data, uuid); !r.Usable {
			b.Fatal(r.Problems)
		}
	}
}

func BenchmarkParseStatusline(b *testing.B) { benchParse(b, "statusline.json") }
func BenchmarkReadStatusline(b *testing.B) {
	data := []byte(fixture(b, "statusline.json"))
	b.SetBytes(int64(len(data)))
	for b.Loop() {
		if _, r := ReadStatusline(data); !r.Usable {
			b.Fatal(r.Problems)
		}
	}
}

func benchParse(b *testing.B, name string) {
	data := []byte(fixture(b, name))
	b.SetBytes(int64(len(data)))
	for b.Loop() {
		if _, _, err := jsonio.ParseObject(data); err != nil {
			b.Fatal(err)
		}
	}
}
