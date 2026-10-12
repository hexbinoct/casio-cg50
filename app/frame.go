package main

import (
	"bytes"
	"image"
	"image/png"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"time"

	"cg50emu"

	"github.com/wailsapp/wails/v2/pkg/runtime"
)

// frameServer is the asset server's handler for /frame (a separate type, so Wails doesn't
// try to bind ServeHTTP for the page).
type frameServer struct{ a *App }

// ServeHTTP serves the screen to the page: GET /frame?since=<gen> returns the whole panel
// (PanelWidth×PanelHeight, 8-bit RGBA) with its generation in X-Frame-Gen, or 204 when the
// panel hasn't changed since <gen> (or nothing is loaded). The page asks once per animation
// frame, so an idle screen costs almost nothing.
func (f frameServer) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	a := f.a
	if r.URL.Path != "/frame" {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	m := a.current()
	if m == nil {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	gen := m.emu.FrameGen()
	if s := r.URL.Query().Get("since"); s != "" {
		if since, err := strconv.ParseUint(s, 10, 64); err == nil && since == gen {
			w.WriteHeader(http.StatusNoContent)
			return
		}
	}
	a.frameMu.Lock()
	defer a.frameMu.Unlock()
	m.emu.FramebufferRGBA(a.frame)
	w.Header().Set("Content-Type", "application/octet-stream")
	w.Header().Set("X-Frame-Gen", strconv.FormatUint(gen, 10))
	w.Write(a.frame)
}

// screenshot renders the panel at an integer scale.
func (a *App) screenshot(scale int) (*image.NRGBA, error) {
	m := a.current()
	if m == nil {
		return nil, errNoDump
	}
	if scale < 1 || scale > 8 {
		scale = 1
	}
	W, H := cg50emu.PanelWidth, cg50emu.PanelHeight
	src := make([]byte, W*H*4)
	m.emu.FramebufferRGBA(src)
	img := image.NewNRGBA(image.Rect(0, 0, W*scale, H*scale))
	for y := 0; y < H*scale; y++ {
		for x := 0; x < W*scale; x++ {
			s := ((y/scale)*W + x/scale) * 4
			copy(img.Pix[y*img.Stride+x*4:], src[s:s+4])
		}
	}
	return img, nil
}

// Screenshot saves the screen as a PNG the user names.
func (a *App) Screenshot(scale int) error {
	img, err := a.screenshot(scale)
	if err != nil {
		return err
	}
	pics, _ := os.UserHomeDir()
	if d := filepath.Join(pics, "Pictures"); dirExists(d) {
		pics = d
	}
	p, err := runtime.SaveFileDialog(a.ctx, runtime.SaveDialogOptions{
		Title:            "Save screenshot",
		DefaultDirectory: pics,
		DefaultFilename:  "cg50-" + time.Now().Format("20060102-150405") + ".png",
		Filters:          []runtime.FileFilter{{DisplayName: "PNG image (*.png)", Pattern: "*.png"}},
	})
	if err != nil || p == "" {
		return err
	}
	var b bytes.Buffer
	if err := png.Encode(&b, img); err != nil {
		return err
	}
	if err := os.WriteFile(p, b.Bytes(), 0o644); err != nil {
		return err
	}
	a.toast("info", "Screenshot saved: "+filepath.Base(p))
	return nil
}

func dirExists(p string) bool {
	st, err := os.Stat(p)
	return err == nil && st.IsDir()
}
