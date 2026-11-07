package cg50emu

import "testing"

// fakeFlash is an image with only the OS header: "CASIOWIN" at 0x20000, version at 0x20020.
func fakeFlash(version string) []byte {
	b := make([]byte, 0x30000)
	for i := range b {
		b[i] = 0xFF
	}
	copy(b[osHeaderOff:], "CASIOWIN")
	copy(b[osVersionOff:], version)
	return b
}

func TestOSVersion(t *testing.T) {
	cases := []struct {
		img            []byte
		version, short string
		supported      bool
	}{
		{fakeFlash("03.60.0000"), "03.60.0000", "3.60", true},
		{fakeFlash("03.60.0202"), "03.60.0202", "3.60", true},
		{fakeFlash("03.80.0000"), "03.80.0000", "3.80", false},
		{fakeFlash("03.50.0000"), "03.50.0000", "3.50", false},
		{fakeFlash("garbage!!!"), "", "", false},
		{make([]byte, 0x30000), "", "", false}, // no CASIOWIN header
		{make([]byte, 0x100), "", "", false},   // too short
	}
	for i, c := range cases {
		v := OSVersion(c.img)
		if v != c.version || ShortOSVersion(v) != c.short || OSSupported(c.img) != c.supported {
			t.Errorf("case %d: got %q %q %v, want %q %q %v", i, v, ShortOSVersion(v),
				OSSupported(c.img), c.version, c.short, c.supported)
		}
	}
}

// The native blitter replaces the OS routine at a 3.60 address: it must stay off on any other
// version (on 3.80 that address holds other code).
func TestEnableHLEOnlyOn360(t *testing.T) {
	for _, c := range []struct {
		version string
		on      bool
	}{{"03.60.0000", true}, {"03.80.0000", false}, {"", false}} {
		e := NewEmulator(fakeFlash(c.version))
		if got := e.EnableHLE(true); got != c.on || (e.cpu.hle != nil) != c.on {
			t.Errorf("OS %q: EnableHLE(true) = %v (hook set %v), want %v", c.version, got, e.cpu.hle != nil, c.on)
		}
		if e.EnableHLE(false) || e.cpu.hle != nil {
			t.Errorf("OS %q: EnableHLE(false) left the hook on", c.version)
		}
	}
}
