package input

import (
	"encoding/binary"
	"testing"
)

// buildEvent serializes an input_event for the parser test.
func buildEvent(typ, code uint16, value int32) []byte {
	b := make([]byte, 24)
	binary.LittleEndian.PutUint64(b[0:8], 123)
	binary.LittleEndian.PutUint64(b[8:16], 456)
	binary.LittleEndian.PutUint16(b[16:18], typ)
	binary.LittleEndian.PutUint16(b[18:20], code)
	binary.LittleEndian.PutUint32(b[20:24], uint32(value))
	return b
}

func TestParseIsActivity(t *testing.T) {
	cases := []struct {
		name string
		ev   []byte
		want bool
	}{
		{"key press", buildEvent(evKey, 30, 1), true},
		{"key release", buildEvent(evKey, 30, 0), true},
		{"mouse move", buildEvent(evRel, 0, -3), true},
		{"mouse wheel", buildEvent(evRel, 8, 1), true},
		{"absolute pos", buildEvent(evAbs, 0, 640), true},
		{"syn report", buildEvent(evSyn, 0, 0), false},
		{"msc scan", buildEvent(4, 4, 1), false},
		{"empty", nil, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := parseIsActivity(c.ev); got != c.want {
				t.Fatalf("parseIsActivity(%s) = %v, want %v", c.name, got, c.want)
			}
		})
	}
}

func TestParseIsActivityMultipleEvents(t *testing.T) {
	var buf []byte
	buf = append(buf, buildEvent(evSyn, 0, 0)...)
	buf = append(buf, buildEvent(evKey, 30, 1)...)
	buf = append(buf, buildEvent(evSyn, 0, 0)...)
	if !parseIsActivity(buf) {
		t.Fatal("expected activity from a key press buried between SYN frames")
	}
}
