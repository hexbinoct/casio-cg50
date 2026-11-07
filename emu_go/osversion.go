package cg50emu

import "bytes"

// The OS's own header in flash: "CASIOWIN" at 0x80020000 and the version string ("03.60.0000")
// 32 bytes after it. Same place in 3.60 and in the 3.80 updater's image (re/os_version.py).
const (
	osHeaderOff  = 0x20000
	osVersionOff = 0x20020
	osVersionLen = 10

	// SupportedOS is the OS version the emulator is built against: a few shortcuts read or
	// replace OS code and data at 3.60 addresses (the native blitter hle.go, the MAIN MENU
	// check install.go uses, the warm-boot flag, the add-in page table the desktop app's
	// analysis reads). Another version still runs on the plain hardware model, untested.
	SupportedOS = "03.60"
)

// OSVersion returns the OS version a flash image carries ("03.60.0000"), or "" if it has no
// recognisable OS header.
func OSVersion(flash []byte) string {
	if len(flash) < osVersionOff+osVersionLen ||
		!bytes.Equal(flash[osHeaderOff:osHeaderOff+8], []byte("CASIOWIN")) {
		return ""
	}
	v := flash[osVersionOff : osVersionOff+osVersionLen]
	for i, c := range v {
		if (i == 2 || i == 5) && c != '.' || i != 2 && i != 5 && (c < '0' || c > '9') {
			return ""
		}
	}
	return string(v)
}

// OSSupported reports whether a flash image carries the OS version the emulator supports.
func OSSupported(flash []byte) bool {
	return len(OSVersion(flash)) == osVersionLen && OSVersion(flash)[:5] == SupportedOS
}

// ShortOSVersion turns "03.60.0000" into "3.60", the way the calculator's Version screen and
// people write it ("" stays "").
func ShortOSVersion(v string) string {
	if len(v) < 5 {
		return v
	}
	return string(bytes.TrimLeft([]byte(v[:5]), "0"))
}

// OSVersion returns the version of the OS in the machine's flash (see the function OSVersion).
func (e *Emulator) OSVersion() string {
	e.mu.Lock()
	defer e.mu.Unlock()
	return OSVersion(e.mem.flash)
}
