package cpio

import (
	"bytes"
	"strconv"
	"testing"
)

func parseNewc(t *testing.T, data []byte) map[string][]byte {
	t.Helper()
	entries := map[string][]byte{}
	off := 0
	for {
		if off+110 > len(data) {
			t.Fatalf("truncated header at %d", off)
		}
		if string(data[off:off+6]) != "070701" {
			t.Fatalf("bad magic at %d: %q", off, data[off:off+6])
		}
		field := func(i int) int {
			v, err := strconv.ParseUint(string(data[off+6+i*8:off+6+i*8+8]), 16, 32)
			if err != nil {
				t.Fatalf("bad field %d: %v", i, err)
			}
			return int(v)
		}
		size := field(6)
		nameLen := field(11)
		name := string(data[off+110 : off+110+nameLen-1])
		off += 110 + nameLen
		off += (4 - off%4) % 4
		body := data[off : off+size]
		off += size
		off += (4 - off%4) % 4
		if name == "TRAILER!!!" {
			return entries
		}
		entries[name] = body
	}
}

func TestWriteNewc(t *testing.T) {
	var buf bytes.Buffer
	err := Write(&buf, []Entry{
		File("preseed.cfg", 0644, []byte("hello")),
		File("clearme-assets/late.sh", 0755, []byte("#!/bin/sh\nexit 0\n")),
	})
	if err != nil {
		t.Fatal(err)
	}
	got := parseNewc(t, buf.Bytes())
	if string(got["preseed.cfg"]) != "hello" {
		t.Errorf("preseed.cfg = %q", got["preseed.cfg"])
	}
	if _, ok := got["clearme-assets"]; !ok {
		t.Error("parent directory entry missing")
	}
	if string(got["clearme-assets/late.sh"]) != "#!/bin/sh\nexit 0\n" {
		t.Errorf("late.sh = %q", got["clearme-assets/late.sh"])
	}
}
