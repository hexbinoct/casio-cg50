package main

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"testing"
)

// The battery-ADC model (periphIRQ, the INTEVT 0x560 source) must behave exactly like the
// Python oracle for the same op sequence (emu/adc_golden.txt, `python emu/adc_selftest.py`).
func TestADCOracleTranscript(t *testing.T) {
	raw, err := os.ReadFile("../emu/adc_golden.txt")
	if err != nil {
		t.Skipf("no transcript golden (%v) — run: python emu/adc_selftest.py", err)
	}
	b := NewMMIOBus()
	cpu := NewCPU(NewMemory(nil, b))
	b.cpu = cpu
	b.timerPeriod = 30000
	num := func(s string, base int) uint64 {
		v, err := strconv.ParseUint(s, base, 64)
		if err != nil {
			t.Fatalf("bad number %q", s)
		}
		return v
	}
	for n, line := range strings.Split(strings.TrimSpace(string(raw)), "\n") {
		f := strings.Fields(line)
		switch f[0] {
		case "ips":
			b.SetInstrPerSecond(num(f[1], 10))
		case "at":
			cpu.cycles, cpu.idle = num(f[1], 10), num(f[2], 10)
		case "w":
			b.Write(0xA4610000+uint32(num(f[1], 16)), 2, uint32(num(f[2], 16)))
		case "tick":
			b.tick(cpu)
		case "r":
			if got, want := b.Read(0xA4610000+uint32(num(f[1], 16)), 2), uint32(num(f[2], 16)); got != want {
				t.Errorf("line %d (%s): read %04x, oracle %04x", n+1, line, got, want)
			}
		case "irq":
			var got []string
			for _, p := range cpu.pending {
				got = append(got, fmt.Sprintf("%x", p.intevt))
			}
			cpu.pending = nil
			if strings.Join(got, " ") != strings.Join(f[1:], " ") {
				t.Errorf("line %d (%s): raised [%s], oracle [%s]", n+1, line, strings.Join(got, " "), strings.Join(f[1:], " "))
			}
		default:
			t.Fatalf("line %d: unknown op %q", n+1, f[0])
		}
	}
}
