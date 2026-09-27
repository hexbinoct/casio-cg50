//go:build probe

package main

// Why does a save-state ignore keys? (cont.18u: the phone's snapshot in the Graph Y= editor
// took no key at any speed, on the phone and on the desktop.) Prints the CPU/KEYSC state,
// then holds RIGHT and reports interrupts taken, wake-ups and the PCs the OS visits.
//   go -C emu_go test -tags probe -run TestStuckStateProbe -v
import (
	"fmt"
	"os"
	"sort"
	"testing"
)

// stackScan lists the code addresses (0x80xxxxxx / 0xa0xxxxxx) in the 96 words above sp —
// the return addresses of the thread's call chain, innermost first.
func stackScan(e *Emulator) string {
	s := ""
	sp := e.cpu.r[15]
	for i := uint32(0); i < 96; i++ {
		v := e.mem.R32(sp + i*4)
		if v>>24 == 0x80 || v>>24 == 0xa0 {
			s += fmt.Sprintf("[+%02x]%08x ", i*4, v)
		}
	}
	return s
}

func TestStuckStateProbe(t *testing.T) {
	flash, _ := os.ReadFile("../os/flash_dump/flash_full.bin")
	st, err := os.ReadFile("../re/_phone_state.bin")
	if err != nil {
		t.Skip("pull the phone state first: python re/phone.py pull")
	}
	// A healthy state for comparison: where does its main thread sleep, what is on its stack?
	if good, err := os.ReadFile("../os/flash_dump/cg50_state.bin"); err == nil {
		g := NewEmulator(flash)
		if err := g.Resume(good); err != nil {
			t.Fatal(err)
		}
		g.Step(3_000_000)
		for !g.cpu.sleeping {
			g.Step(1)
		}
		t.Logf("GOOD state asleep: pc=%08x sr=%08x pr=%08x r15=%08x", g.cpu.pc, g.cpu.sr, g.cpu.pr, g.cpu.r[15])
		t.Logf("GOOD stack: %s", stackScan(g))
	}
	e := NewEmulator(flash)
	if err := e.Resume(st); err != nil {
		t.Fatal(err)
	}
	e.SetInstrPerSecond(100_000_000)
	c, k := e.cpu, e.mmio.keysc
	t.Logf("BAD pr=%08x r15=%08x  stack: %s", c.pr, c.r[15], stackScan(e))
	t.Logf("pc=%08x sr=%08x (BL=%d IMASK=%x) sleeping=%v pending=%v vbr=%08x", c.pc, c.sr,
		(c.sr>>28)&1, (c.sr>>4)&0xF, c.sleeping, c.pending, c.vbr)
	t.Logf("KEYSC ctrl=%04x mode=%04x ie=%04x scanPeriod=%d", k.ctrl, k.mode, k.ie, k.scanPeriod)
	t.Logf("RTC rcr2=%02x  timerPeriod=%d adcMode=%v", e.mmio.rtc.rcr2, e.mmio.timerPeriod, e.mmio.periphIRQ.adcMode)
	irq0, idle0 := c.irqCnt, c.idle
	pcs := map[uint32]int{}
	e.KeyDown(1, 7)
	for i := 0; i < 120_000_000; i++ {
		if !c.sleeping {
			pcs[c.pc&^0xF]++
		}
		e.Step(1)
	}
	e.KeyUp(1, 7)
	t.Logf("after 1.2 s with RIGHT held: irqs=%d idle=%d/120M pushes=%d pc=%08x sleeping=%v",
		c.irqCnt-irq0, c.idle-idle0, e.Pushes(), c.pc, c.sleeping)
	type kv struct {
		a uint32
		n int
	}
	var l []kv
	for a, n := range pcs {
		l = append(l, kv{a, n})
	}
	sort.Slice(l, func(i, j int) bool { return l[i].n > l[j].n })
	for i, x := range l {
		if i >= 25 {
			break
		}
		t.Logf("  pc %08x: %d", x.a, x.n)
	}
}
