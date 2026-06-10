package main

// Interactive web UI for the emulator: runs the SH7305 core, auto-drives first-boot
// setup to the MAIN MENU, then serves the live framebuffer to a browser and injects
// the keys you press (mapped to the real fx-CG50 matrix; see re/KEYMAP.md).
//
//   go -C emu_go run . 0 30000 web
//   -> open http://127.0.0.1:8080 , click the page, and type.
//
// Zero external deps: net/http + image/png from the stdlib; the browser is the window.

import (
	"fmt"
	"image"
	"image/color"
	"image/png"
	"net"
	"net/http"
	"os"
)

const fbW, fbH = 384, 216 // fx-CG50 usable framebuffer (phys 0x0c000000 = start of DRAM)

// alphaKey is the ALPHA modifier (grid C8R7); letters are sent as ALPHA + letter.
var alphaKey = [2]uint32{6, 7}

// webKeyMap: browser KeyboardEvent.key -> sequence of matrix presses {row,col} (0-based).
var webKeyMap = map[string][][2]uint32{
	"0": {{6, 1}}, "1": {{6, 2}}, "2": {{5, 2}}, "3": {{4, 2}},
	"4": {{6, 3}}, "5": {{5, 3}}, "6": {{4, 3}},
	"7": {{6, 4}}, "8": {{5, 4}}, "9": {{4, 4}},
	".": {{5, 1}},
	"+": {{3, 2}}, "-": {{2, 2}}, "*": {{3, 3}}, "/": {{2, 3}},
	"(": {{4, 5}}, ")": {{3, 5}}, ",": {{2, 5}},
	"Enter": {{2, 1}}, "Backspace": {{3, 4}}, "Delete": {{3, 4}},
	"Escape": {{3, 8}}, "Home": {{3, 7}},
	"ArrowUp": {{1, 8}}, "ArrowDown": {{2, 7}}, "ArrowLeft": {{2, 8}}, "ArrowRight": {{1, 7}},
	"F1": {{6, 9}}, "F2": {{5, 9}}, "F3": {{4, 9}}, "F4": {{3, 9}}, "F5": {{2, 9}}, "F6": {{1, 9}},
	"Tab": {{6, 8}}, // SHIFT
	"`":   {{6, 7}}, // ALPHA
	"^":   {{4, 7}}, // power
	"=":   {{6, 8}}, // (SHIFT) — convenience
}

// letterCoord: ALPHA-layer letter A..Z matrix position {row,col}.
var letterCoord = map[byte][2]uint32{
	'a': {6, 6}, 'b': {5, 6}, 'c': {4, 6}, 'd': {3, 6}, 'e': {2, 6}, 'f': {1, 6},
	'g': {6, 5}, 'h': {5, 5}, 'i': {4, 5}, 'j': {3, 5}, 'k': {2, 5}, 'l': {1, 5},
	'm': {6, 4}, 'n': {5, 4}, 'o': {4, 4}, 'p': {6, 3}, 'q': {5, 3}, 'r': {4, 3},
	's': {3, 3}, 't': {2, 3}, 'u': {6, 2}, 'v': {5, 2}, 'w': {4, 2}, 'x': {3, 2},
	'y': {2, 2}, 'z': {6, 1},
}

func resolveKey(k string) [][2]uint32 {
	if len(k) == 1 {
		c := k[0]
		if c >= 'A' && c <= 'Z' {
			c += 32
		}
		if c >= 'a' && c <= 'z' {
			return [][2]uint32{alphaKey, letterCoord[c]}
		}
	}
	return webKeyMap[k]
}

// writeFramePNG renders the live framebuffer straight from the DRAM byte slice
// (RGB565, big-endian). Reading concurrently with the CPU goroutine is benign
// (worst case a torn pixel) and avoids the memory-routing maps entirely.
func writeFramePNG(w http.ResponseWriter, mem *Memory) {
	img := image.NewRGBA(image.Rect(0, 0, fbW, fbH))
	d := mem.dram
	for y := 0; y < fbH; y++ {
		for x := 0; x < fbW; x++ {
			o := (y*fbW + x) * 2
			var p uint16
			if o+1 < len(d) {
				p = uint16(d[o])<<8 | uint16(d[o+1])
			}
			img.SetRGBA(x, y, color.RGBA{
				uint8((p>>11)&0x1F) << 3,
				uint8((p>>5)&0x3F) << 2,
				uint8(p&0x1F) << 3,
				255,
			})
		}
	}
	w.Header().Set("Content-Type", "image/png")
	w.Header().Set("Cache-Control", "no-store")
	png.Encode(w, img)
}

const webPage = `<!doctype html><html><head><meta charset=utf-8><title>fx-CG50</title>
<style>
 body{background:#1b1b1b;color:#ddd;font-family:Consolas,monospace;text-align:center;margin-top:14px}
 #screen{image-rendering:pixelated;width:768px;height:432px;border:10px solid #000;border-radius:6px;background:#000}
 .legend{display:inline-block;text-align:left;margin-top:12px;font-size:13px;color:#9bd}
 b{color:#fff}
</style></head><body>
<h3>fx-CG50 emulator &mdash; click the screen, then type</h3>
<img id=screen src="/frame"><br>
<button onclick="act('/save')">Save State (F9)</button>
<button onclick="act('/load')">Reload State (F10)</button>
<span id=status></span>
<div class=legend>
<b>0-9 . + - * / ( ) ,</b> those keys &nbsp;|&nbsp; <b>Enter</b>=EXE &nbsp; <b>Backspace</b>=DEL
&nbsp; <b>Esc</b>=EXIT &nbsp; <b>Home</b>=MENU<br>
<b>Arrows</b>=cursor &nbsp; <b>F1-F6</b>=function keys &nbsp; <b>Tab</b>=SHIFT &nbsp;
<b>` + "`" + `</b>=ALPHA &nbsp; <b>a-z</b>=ALPHA+letter &nbsp;|&nbsp; <b>F9</b>=save state &nbsp; <b>F10</b>=reload state
</div>
<script>
 var img=document.getElementById('screen'), tmp=new Image(), n=0, busy=false;
 tmp.onload=function(){ img.src=tmp.src; busy=false; };
 tmp.onerror=function(){ busy=false; };
 setInterval(function(){ if(!busy){ busy=true; tmp.src='/frame?'+(n++); } }, 60);
 function act(u){ fetch(u).then(function(r){return r.text()}).then(function(t){
   var s=document.getElementById('status'); s.textContent=' '+t;
   setTimeout(function(){s.textContent='';},2500); }); }
 document.addEventListener('keydown', function(e){
   if(e.key==='F9'){ act('/save'); e.preventDefault(); return; }
   if(e.key==='F10'){ act('/load'); e.preventDefault(); return; }
   fetch('/key?k='+encodeURIComponent(e.key));
   e.preventDefault();
 });
</script></body></html>`

// runWeb is the interactive entry point. Never returns (runs until the process is killed).
// resumed = the machine was already restored from a save-state (so skip the scripted
// first-boot drive — we're already at the MAIN MENU).
func runWeb(cpu *CPU, mem *Memory, mmio *MMIOBus, resumed bool) {
	if mmio.timerPeriod == 0 {
		mmio.timerPeriod = 30000 // proven boot timer cadence
	}
	coordCh := make(chan [2]uint32, 512)
	// Save/Load requests from the HTTP handlers; the CPU goroutine performs them at a step
	// boundary so there's no race on dram/flash. Each is a size-1 channel carrying the reply
	// channel the handler blocks on (so the button shows the real result).
	type ssReq struct{ reply chan string }
	saveCh := make(chan ssReq, 1)
	loadCh := make(chan ssReq, 1)

	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		fmt.Fprint(w, webPage)
	})
	mux.HandleFunc("/frame", func(w http.ResponseWriter, r *http.Request) { writeFramePNG(w, mem) })
	mux.HandleFunc("/key", func(w http.ResponseWriter, r *http.Request) {
		for _, c := range resolveKey(r.URL.Query().Get("k")) {
			select {
			case coordCh <- c:
			default:
			}
		}
		w.WriteHeader(http.StatusNoContent)
	})
	ssHandler := func(ch chan ssReq) http.HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) {
			reply := make(chan string, 1)
			select {
			case ch <- ssReq{reply}:
				fmt.Fprint(w, <-reply)
			default:
				fmt.Fprint(w, "busy, try again")
			}
		}
	}
	mux.HandleFunc("/save", ssHandler(saveCh))
	mux.HandleFunc("/load", ssHandler(loadCh))
	// Bind a listener: try the preferred port (override with a 5th CLI arg, e.g.
	// `run . 0 30000 web 9000`), then a few alternates, then let the OS pick any free
	// port (Windows reserves/excludes some ranges -> "forbidden" bind errors).
	webAddr := "127.0.0.1:8080"
	if len(os.Args) > 4 {
		webAddr = "127.0.0.1:" + os.Args[4]
	}
	var ln net.Listener
	for _, addr := range []string{webAddr, "127.0.0.1:8123", "127.0.0.1:8973", "127.0.0.1:0"} {
		l, err := net.Listen("tcp", addr)
		if err == nil {
			ln = l
			break
		}
		fmt.Printf("  (port %s unavailable: %v)\n", addr, err)
	}
	if ln == nil {
		fmt.Println("could not bind any TCP port for the web UI")
		return
	}
	go func() {
		if err := http.Serve(ln, mux); err != nil {
			fmt.Println("web server:", err)
		}
	}()
	if resumed {
		fmt.Printf("Web UI ready -> open http://%s  (RESUMED from save-state — keyboard live now)\n", ln.Addr().String())
	} else {
		fmt.Printf("Web UI ready -> open http://%s  (auto-booting to MAIN MENU, ~5s)\n", ln.Addr().String())
	}

	// Scripted first-boot -> MAIN MENU (the proven cont.12 sequence): fixed-timing presses.
	// Skipped entirely when we resumed from a save-state (already at the menu).
	type sk struct {
		at       uint64
		row, col uint32
	}
	pressAt, interval := uint64(130_000_000), uint64(14_000_000)
	menuSeq := [][2]uint32{{1, 9}, {1, 9}, {1, 9}, {1, 9}, {6, 9}, {6, 9}, {1, 9}, {1, 9}, {2, 1}}
	var script []sk
	if !resumed {
		for i, k := range menuSeq {
			script = append(script, sk{pressAt + uint64(i)*interval, k[0], k[1]})
		}
	}
	si := 0

	// User-key injection state: one matrix press at a time, DECODE-CONFIRMED — after
	// injecting we wait until the OS decode FUN_801952cc actually runs for the key (re-
	// injecting if it doesn't), so a press is never lost to a redraw flush. Multi-press
	// sequences (ALPHA+letter) thus land reliably in order.
	var have, injected, sawDecode bool
	var cur [2]uint32
	var injStart uint64
	retries := 0

	defer func() {
		if r := recover(); r != nil {
			fmt.Printf("\nCPU FAULT @0x%08x after %d instr: %v\n(web server still serving the last frame; Ctrl+C to quit)\n", cpu.pc, cpu.cycles, r)
			select {} // keep the HTTP server alive so the final screen stays viewable
		}
	}()

	for {
		mmio.tick(cpu)
		cpu.step()

		// Save/Load requested from the UI — done here (CPU goroutine) so there's no race.
		select {
		case req := <-saveCh:
			if err := SaveState(statePath, cpu, mem); err != nil {
				req.reply <- "save failed: " + err.Error()
			} else {
				fmt.Printf("save-state: snapshot written to %s (pc=0x%08x)\n", statePath, cpu.pc)
				req.reply <- "state saved"
			}
		case req := <-loadCh:
			if err := LoadState(statePath, cpu, mem); err != nil {
				req.reply <- "load failed: " + err.Error()
			} else {
				cpu.cycles, mmio.timerNext, mmio.timerTicks = 0, mmio.timerPeriod, 0
				cpu.pending = nil
				si = len(script)              // don't re-run the boot script
				have, injected = false, false // drop any in-flight keypress
				fmt.Printf("save-state: reloaded %s (pc=0x%08x)\n", statePath, cpu.pc)
				req.reply <- "state reloaded"
			}
		default:
		}

		if si < len(script) {
			if cpu.cycles >= script[si].at && keySafe(cpu) {
				injectKey(cpu, mem, script[si].row, script[si].col)
				si++
				if si == len(script) {
					fmt.Println("MAIN MENU reached - keyboard is now live.")
				}
			}
			continue // ignore user input until setup is driven to the menu
		}

		if !have {
			select {
			case c := <-coordCh:
				have, cur, injected = true, c, false
			default:
			}
		}
		if have && !injected {
			if keySafe(cpu) {
				injectKey(cpu, mem, cur[0], cur[1])
				injected, sawDecode, injStart, retries = true, false, cpu.cycles, 0
			}
		} else if have { // injected, waiting for the app to actually decode it
			if cpu.pc >= 0x801952cc && cpu.pc < 0x801952e0 {
				sawDecode = true
			}
			if sawDecode {
				have = false // landed
			} else if cpu.cycles-injStart > 40_000_000 && keySafe(cpu) {
				retries++
				if retries > 8 {
					have = false // give up (key likely a no-op in this context)
				} else {
					injectKey(cpu, mem, cur[0], cur[1])
					injStart = cpu.cycles
				}
			}
		}
	}
}
