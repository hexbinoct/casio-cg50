//go:build probe

package main

// Probe (2026-10-10): ~/main/casio/wolf3d_cg50 runs Kelli's fx-9860 Wolfenstein 3D (.g1a) on the
// CG50 through a shim (the game's code unchanged at 0x00300200, its syscalls and LCD routine
// redirected). This installs build/Wolf3D.g3a on the 32 MB image, launches it, plays WOLF_KEYS
// and reports what the shim saw. Run it through wolf3d_cg50/tools/emu_run.py, which passes the
// shim's symbol addresses:
//   WOLF_G3A    the .g3a (default ../../wolf3d_cg50/build/Wolf3D.g3a)
//   WOLF_SYMS   frames=<hex>,unknown=<hex>,trace=<hex>,tracepos=<hex>
//   WOLF_KEYS   comma list: "row-col" (re/KEYMAP.md inject coords), "hold:row-col:N" (held for
//               N x 10 M instructions), "wait:N" (N x 100 M instructions), "shot"
//   frames: $TMPDIR/cg50_wolf_NN.png

import (
	"fmt"
	"image"
	"image/color"
	"image/png"
	"os"
	"strings"
	"testing"
)

func wolfSyms(t *testing.T) map[string]uint32 {
	m := map[string]uint32{}
	for _, kv := range strings.Split(os.Getenv("WOLF_SYMS"), ",") {
		var k string
		var v uint32
		if i := strings.IndexByte(kv, '='); i > 0 {
			k = kv[:i]
			fmt.Sscanf(kv[i+1:], "%x", &v)
			m[k] = v
		}
	}
	return m
}

func wolfReport(t *testing.T, e *Emulator, syms map[string]uint32, what string) {
	rd := func(a uint32) uint32 { return e.mem.Read(a, 4) }
	t.Logf("[%s] pc %08x pr %08x sp %08x frames %d fault %q", what, e.cpu.pc, e.cpu.pr, e.cpu.r[15],
		rd(syms["frames"]), e.Fault())
	var unk []string
	for i := uint32(0); i < 32; i++ {
		n, c := rd(syms["unknown"]+8*i), rd(syms["unknown"]+8*i+4)
		if c == 0 {
			break
		}
		unk = append(unk, fmt.Sprintf("%#x x%d", n, c))
	}
	t.Logf("  unknown syscalls: %s", strings.Join(unk, ", "))
	if a := syms["pushsrc"]; a != 0 {
		var ps []string
		for i := uint32(0); i < 8 && rd(a+4*i) != 0; i++ {
			ps = append(ps, fmt.Sprintf("%08x", rd(a+4*i)))
		}
		t.Logf("  pushed buffers: %s", strings.Join(ps, " "))
	}
	pos := rd(syms["tracepos"])
	var tr []string
	for i := uint32(0); i < 16 && i < pos; i++ {
		j := (pos - 1 - i) % 128
		a := syms["trace"] + 16*j
		tr = append(tr, fmt.Sprintf("%#x(%x,%x)@%x", rd(a), rd(a+4), rd(a+8), rd(a+12)))
	}
	t.Logf("  last syscalls (newest first, %d total): %s", pos, strings.Join(tr, " "))
}

// wolfSaveVRAM saves the live VRAM (what the shim drew), next to savePNG's panel contents.
func wolfSaveVRAM(t *testing.T, e *Emulator, name string) {
	buf := make([]byte, fbBytes)
	e.VRAMRGB565(buf)
	img := image.NewRGBA(image.Rect(0, 0, FbWidth, FbHeight))
	for p := 0; p < FbWidth*FbHeight; p++ {
		c := uint16(buf[2*p])<<8 | uint16(buf[2*p+1])
		img.SetRGBA(p%FbWidth, p/FbWidth, color.RGBA{uint8(c>>11) << 3, uint8(c>>5&0x3F) << 2, uint8(c&0x1F) << 3, 255})
	}
	f, err := os.Create(fmt.Sprintf("%s/cg50_%s.png", os.TempDir(), name))
	if err != nil {
		t.Log(err)
		return
	}
	png.Encode(f, img)
	f.Close()
	t.Logf("vram pixel (0,100) = %04x, (0,5) = %04x", uint16(buf[2*(100*FbWidth)])<<8|uint16(buf[2*(100*FbWidth)+1]),
		uint16(buf[2*(5*FbWidth)])<<8|uint16(buf[2*(5*FbWidth)+1]))
}

func TestWolf(t *testing.T) {
	path := os.Getenv("WOLF_G3A")
	if path == "" {
		path = "../../wolf3d_cg50/build/Wolf3D.g3a"
	}
	g3a, err := os.ReadFile(path)
	if err != nil {
		t.Skip("no Wolf3D.g3a: ", err)
	}
	syms := wolfSyms(t)
	e := installState(t)
	s := &rcSession{t, e}
	if err := e.InstallAddin("Wolf3D.g3a", g3a); err != nil {
		t.Fatal(err)
	}
	e.Step(20_000_000)
	s.launch(-1)
	for n := 0; e.cpu.pc != 0x00300000; n++ {
		if n > 200_000_000 || e.Fault() != "" {
			t.Fatalf("never started (%s)", e.Fault())
		}
		e.Step(1)
	}
	t.Logf("entry: r4 %x r5 %x sp %08x", e.cpu.r[4], e.cpu.r[5], e.cpu.r[15])
	shot := 0
	save := func() {
		savePNG(t, e, fmt.Sprintf("wolf_%02d", shot))
		wolfSaveVRAM(t, e, fmt.Sprintf("wolf_%02d_vram", shot))
		shot++
	}
	run := func(n int) {
		for i := 0; i < n && e.Fault() == ""; i++ {
			e.Step(100_000_000)
		}
	}
	run(3)
	wolfReport(t, e, syms, "after start")
	save()
	for _, k := range strings.Split(os.Getenv("WOLF_KEYS"), ",") {
		if e.Fault() != "" {
			break
		}
		var r, c, n uint32
		switch {
		case k == "":
		case k == "shot":
			save()
			wolfReport(t, e, syms, fmt.Sprintf("shot %d", shot-1))
		case strings.HasPrefix(k, "press:"):
			fmt.Sscanf(k, "press:%d-%d", &r, &c)
			e.KeyDown(r, c)
		case strings.HasPrefix(k, "release:"):
			fmt.Sscanf(k, "release:%d-%d", &r, &c)
			e.KeyUp(r, c)
		case strings.HasPrefix(k, "crashlog:"):
			// single-step up to N x 1 M instructions; on a fault, print the last jumps
			fmt.Sscanf(k, "crashlog:%d", &n)
			type jmp struct{ from, to, sp, r0 uint32 }
			var ring [48]jmp
			pos := 0
			prev := e.cpu.pc
			for i := 0; i < int(n)*1_000_000 && e.Fault() == ""; i++ {
				e.Step(1)
				if pc := e.cpu.pc; pc != prev+2 && pc != prev+4 {
					ring[pos%48] = jmp{prev, pc, e.cpu.r[15], e.cpu.r[0]}
					pos++
				}
				prev = e.cpu.pc
			}
			for i := 0; i < 48 && i < pos; i++ {
				j := ring[(pos-48+i+48*2)%48]
				if pos < 48 {
					j = ring[i]
				}
				t.Logf("  %08x -> %08x  sp %08x r0 %08x", j.from, j.to, j.sp, j.r0)
			}
		case strings.HasPrefix(k, "keylog:"):
			// single-step N x 1 M instructions, logging IsKeyDown(keycode) results (0x307ae8)
			fmt.Sscanf(k, "keylog:%d", &n)
			type kr struct{ code, res uint32 }
			seen := map[kr]int{}
			var code uint32
			pending := false
			for i := 0; i < int(n)*1_000_000 && e.Fault() == ""; i++ {
				e.Step(1)
				switch e.cpu.pc {
				case 0x307ae8:
					code, pending = e.cpu.r[4], true
				case 0x307b12, 0x307b06:
					if pending {
						seen[kr{code, e.cpu.r[0]}]++
						pending = false
					}
				}
			}
			var out []string
			for k, v := range seen {
				out = append(out, fmt.Sprintf("%d->%d x%d", k.code, k.res, v))
			}
			t.Logf("IsKeyDown: %s", strings.Join(out, ", "))
			t.Logf("KEYSC %04x %04x %04x %04x %04x %04x", e.mem.Read(0xA44B0000, 2), e.mem.Read(0xA44B0002, 2),
				e.mem.Read(0xA44B0004, 2), e.mem.Read(0xA44B0006, 2), e.mem.Read(0xA44B0008, 2), e.mem.Read(0xA44B000A, 2))
		case strings.HasPrefix(k, "wait:"):
			fmt.Sscanf(k, "wait:%d", &n)
			run(int(n))
		case strings.HasPrefix(k, "hold:"):
			fmt.Sscanf(k, "hold:%d-%d:%d", &r, &c, &n)
			e.KeyDown(r, c)
			for i := uint32(0); i < n && e.Fault() == ""; i++ {
				e.Step(10_000_000)
			}
			e.KeyUp(r, c)
			e.Step(10_000_000)
		default:
			if _, err := fmt.Sscanf(k, "%d-%d", &r, &c); err == nil {
				e.InjectKey(r, c)
				e.Step(30_000_000)
			}
		}
	}
	run(2)
	save()
	wolfReport(t, e, syms, "end")
	t.Logf("at the MAIN MENU: %v", e.atMainMenu())
}
