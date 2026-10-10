package proc

import (
	"encoding/binary"
	"errors"
	"testing"
)

// kinfoWith is a kinfo_proc buffer of the size macOS gives, zero but for the
// controlling terminal's device.
func kinfoWith(dev uint32) []byte {
	b := make([]byte, kinfoSize)
	binary.NativeEndian.PutUint32(b[kinfoTdevOff:], dev)
	return b
}

func TestParseTdev(t *testing.T) {
	for _, tc := range []struct {
		name string
		buf  []byte
		dev  uint64
		err  error
	}{
		{"iTerm2 pane", kinfoWith(268435461), 268435461, nil}, // 16/5, as measured
		{"device zero", kinfoWith(0), 0, nil},
		{"no terminal", kinfoWith(noDev), 0, ErrNoTTY},
		{"no such process", nil, 0, ErrNoProcess},
		{"short", make([]byte, kinfoSize-1), 0, errMalformed},
		{"long", make([]byte, kinfoSize+1), 0, errMalformed},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dev, err := parseTdev(tc.buf)
			if dev != tc.dev || !errors.Is(err, tc.err) {
				t.Errorf("got %d, %v; want %d, %v", dev, err, tc.dev, tc.err)
			}
		})
	}
}

// A fixture with the bytes where macOS puts them: the device in little-endian
// at byte 572 of 648.
func TestParseTdevLayout(t *testing.T) {
	if binary.NativeEndian.Uint32([]byte{1, 0, 0, 0}) != 1 {
		t.Skip("the literal fixture is little-endian")
	}
	b := make([]byte, 648)
	copy(b[572:], []byte{0x05, 0x00, 0x00, 0x10})
	if dev, err := parseTdev(b); dev != 0x10000005 || err != nil {
		t.Errorf("got %#x, %v", dev, err)
	}
}
