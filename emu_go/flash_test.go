package main

import (
	"bufio"
	"fmt"
	"os"
	"testing"
)

// TestFlashGolden replays emu/flash_golden.txt (emu/flash_selftest.py, the Python oracle) against
// the Go flash model: every read must return what the oracle read.
func TestFlashGolden(t *testing.T) {
	f, err := os.Open("../emu/flash_golden.txt")
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	image := make([]byte, 0x80000)
	for i := range image {
		image[i] = byte(i*7 + 3)
	}
	m := NewMemory(image, NewMMIOBus())
	sc := bufio.NewScanner(f)
	for n := 1; sc.Scan(); n++ {
		var op string
		var addr, a, b uint32
		if k, _ := fmt.Sscanf(sc.Text(), "%s %x %x %x", &op, &addr, &a, &b); k < 3 {
			t.Fatalf("line %d: %q", n, sc.Text())
		}
		switch op {
		case "w":
			m.Write(addr, 2, a)
		case "r":
			if v := m.Read(addr, a); v != b {
				t.Errorf("line %d: read %d bytes at %#x = %#x, oracle %#x", n, a, addr, v, b)
			}
		}
	}
}
