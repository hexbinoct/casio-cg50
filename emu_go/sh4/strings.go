package sh4

import (
	"strings"
	"unicode/utf8"
)

// Encoding is a text encoding the string scanner looks for (a bit set).
type Encoding uint8

const (
	EncASCII Encoding = 1 << iota // printable ASCII, tab, CR, LF
	// EncCasio is the OS's multibyte text: ASCII plus two-byte characters whose lead byte is
	// 0x7F, 0xE5, 0xE6, 0xE7, 0xF7 or 0xF9 (the CG50's extended characters and commands).
	EncCasio
	EncUTF16 // UTF-16 big-endian on even addresses (the OS's file names): ASCII and Latin-1
	EncAll   = EncASCII | EncCasio | EncUTF16
)

func (e Encoding) String() string {
	switch e {
	case EncASCII:
		return "ascii"
	case EncCasio:
		return "casio"
	case EncUTF16:
		return "utf16"
	}
	return "enc?"
}

// String is a string found in memory.
type String struct {
	Addr uint32
	Size int      // bytes, without the terminator
	Enc  Encoding // one of EncASCII, EncCasio, EncUTF16
	// Text: ASCII as is (a backslash doubled), a Casio two-byte character as \xHH\xHH, UTF-16
	// as UTF-8.
	Text       string
	Chars      int  // characters
	Terminated bool // followed by a NUL (byte or unit)
}

// StringOptions say what to look for.
type StringOptions struct {
	MinLen  int      // characters (default 4)
	Enc     Encoding // default EncAll
	NeedNUL bool     // only NUL-terminated strings
	Max     int      // stop after this many (0 = no limit)
	// Loose keeps every run of text characters; by default a string must look like text: at
	// least one letter, mostly letters, digits and spaces, and not one character repeated.
	Loose bool
}

// Byte classes for the byte scan.
const (
	cNone   = 0
	cPrint  = 1 // printable ASCII other than letters, digits, space
	cAlnum  = 2 // letter or digit
	cLetter = 3 // letter (also counts as alnum)
	cSpace  = 4 // space, tab, CR, LF
	cLead   = 5 // a Casio lead byte
)

var byteClass = func() (t [256]uint8) {
	for c := 0x20; c < 0x7f; c++ {
		switch {
		case c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z':
			t[c] = cLetter
		case c >= '0' && c <= '9':
			t[c] = cAlnum
		case c == ' ':
			t[c] = cSpace
		default:
			t[c] = cPrint
		}
	}
	t['\t'], t['\n'], t['\r'] = cSpace, cSpace, cSpace
	for _, c := range []int{0x7f, 0xe5, 0xe6, 0xe7, 0xf7, 0xf9} {
		t[c] = cLead
	}
	return
}()

// runStats are counted while a run grows; the text test runs on candidates only.
type runStats struct {
	chars, multi int
	r            []rune // the characters (a two-byte Casio character as one, lead<<8|byte)
}

func (r *runStats) add(ch rune) {
	r.chars++
	r.r = append(r.r, ch)
}

func isLetter(c rune) bool { return c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' }
func isUpper(c rune) bool  { return c >= 'A' && c <= 'Z' }
func isLower(c rune) bool  { return c >= 'a' && c <= 'z' }

// hasWord: a word of three or more ASCII letters, splitting at case changes the way camelCase
// reads (GetVRAMAddress = Get VRAM Address): an upper-case run, or one capital and lower-case
// letters, or lower-case letters.
func hasWord(r []rune) bool {
	for i := 0; i < len(r); {
		j := i
		switch {
		case isUpper(r[i]):
			for j < len(r) && isUpper(r[j]) {
				j++
			}
			if j-i == 1 {
				for j < len(r) && isLower(r[j]) {
					j++
				}
			}
		case isLower(r[i]):
			for j < len(r) && isLower(r[j]) {
				j++
			}
		default:
			i++
			continue
		}
		if j-i >= 3 {
			return true
		}
		i = j
	}
	return false
}

// ok: long enough and, unless Loose, text-like: a word (hasWord), mostly letters, digits and spaces (70%),
// letters for at least half of the other characters, no character more than 40% of the
// non-space ones, at most one lower-to-upper case change per six letters (camelCase is fine,
// random bytes are not); a Casio string has at most a third two-byte characters and three
// ASCII letters, a UTF-16 one ASCII letters for half its characters.
func (r *runStats) ok(opt *StringOptions, enc Encoding) bool {
	if r.chars < opt.MinLen {
		return false
	}
	if opt.Loose {
		return true
	}
	var letters, latin, textual, nonSpace, flips int
	var cnt [256]uint8
	most := 0
	prev := rune(0)
	for _, c := range r.r {
		switch {
		case isLetter(c):
			letters++
			textual++
			if isUpper(c) && isLower(prev) {
				flips++
			}
		case c >= '0' && c <= '9':
			textual++
		case c == ' ' || c == '\t' || c == '\n' || c == '\r':
			textual++
			prev = c
			continue
		case c >= 0xC0 && c <= 0xFF && c != 0xD7 && c != 0xF7 && enc == EncUTF16:
			latin++
			textual++
		}
		prev = c
		nonSpace++
		if c < 256 {
			if cnt[c] < 255 {
				cnt[c]++
			}
			if int(cnt[c]) > most {
				most = int(cnt[c])
			}
		}
	}
	if letters+latin == 0 || textual*10 < r.chars*7 || (letters+latin)*2 < nonSpace {
		return false
	}
	if nonSpace >= 5 && most*10 > nonSpace*4 {
		return false
	}
	if flips > 1 && flips*6 > letters {
		return false
	}
	if !hasWord(r.r) {
		return false
	}
	switch enc {
	case EncCasio:
		return r.multi*3 <= r.chars && letters >= 3
	case EncUTF16:
		return letters*2 >= r.chars
	}
	return true
}

// FindStrings scans img from start to end (0, 0 = all of it) for strings.
func FindStrings(img *Image, start, end uint32, opt StringOptions) []String {
	if start == 0 && end == 0 {
		start, end = img.Base, img.End()
	}
	if start < img.Base {
		start = img.Base
	}
	if end > img.End() {
		end = img.End()
	}
	if end <= start {
		return nil
	}
	return findStrings(start, img.Data[start-img.Base:end-img.Base], opt)
}

func findStrings(base uint32, data []byte, opt StringOptions) []String {
	if opt.MinLen <= 0 {
		opt.MinLen = 4
	}
	if opt.Enc == 0 {
		opt.Enc = EncAll
	}
	var out []String
	full := func() bool { return opt.Max > 0 && len(out) >= opt.Max }
	if opt.Enc&(EncASCII|EncCasio) != 0 {
		out = scanBytes(base, data, &opt, out)
	}
	if opt.Enc&EncUTF16 != 0 && !full() {
		u := scanUTF16(base, data, &opt, nil)
		out = mergeByAddr(out, u)
	}
	if opt.Max > 0 && len(out) > opt.Max {
		out = out[:opt.Max]
	}
	return out
}

func scanBytes(base uint32, data []byte, opt *StringOptions, out []String) []String {
	casio := opt.Enc&EncCasio != 0
	ascii := opt.Enc&EncASCII != 0
	runs := make([]rune, 0, 256)
	n := len(data)
	for i := 0; i < n; {
		cls := byteClass[data[i]]
		if cls == cNone || cls == cLead && !casio {
			i++
			continue
		}
		st := runStats{r: runs[:0]}
		j := i
		for j < n {
			c := data[j]
			k := byteClass[c]
			if k == cLead {
				if !casio || j+1 >= n || data[j+1] == 0 {
					break
				}
				st.add(rune(c)<<8 | rune(data[j+1]))
				st.multi++
				j += 2
				continue
			}
			if k == cNone {
				break
			}
			st.add(rune(c))
			j++
		}
		term := j < n && data[j] == 0
		enc := EncASCII
		if st.multi > 0 {
			enc = EncCasio
		}
		if (enc == EncCasio || ascii) && (!opt.NeedNUL || term) && st.ok(opt, enc) {
			out = append(out, String{Addr: base + uint32(i), Size: j - i, Enc: enc,
				Text: byteText(data[i:j]), Chars: st.chars, Terminated: term})
			if opt.Max > 0 && len(out) >= opt.Max {
				return out
			}
		}
		runs = st.r
		if j == i {
			j++
		}
		i = j
	}
	return out
}

// byteText renders ASCII / Casio bytes: printable ASCII as is (a backslash doubled), tab, CR
// and LF as \t \r \n, a two-byte character and any other byte as \xHH.
func byteText(b []byte) string {
	var s strings.Builder
	s.Grow(len(b))
	hex := func(c byte) {
		s.WriteString(`\x`)
		s.WriteByte("0123456789abcdef"[c>>4])
		s.WriteByte("0123456789abcdef"[c&15])
	}
	for i := 0; i < len(b); i++ {
		c := b[i]
		switch {
		case byteClass[c] == cLead && i+1 < len(b): // a two-byte character
			hex(c)
			hex(b[i+1])
			i++
		case c == '\\':
			s.WriteString(`\\`)
		case c >= 0x20 && c < 0x7f:
			s.WriteByte(c)
		case c == '\n':
			s.WriteString(`\n`)
		case c == '\r':
			s.WriteString(`\r`)
		case c == '\t':
			s.WriteString(`\t`)
		default:
			hex(c)
		}
	}
	return s.String()
}

func utf16Class(u uint16) uint8 {
	switch {
	case u < 0x80:
		return byteClass[u]
	case u >= 0xC0 && u <= 0xFF && u != 0xD7 && u != 0xF7: // Latin-1 letters
		return cLetter
	case u >= 0xA0 && u < 0xC0:
		return cPrint
	}
	return cNone
}

func scanUTF16(base uint32, data []byte, opt *StringOptions, out []String) []String {
	start := int(base & 1) // even addresses only
	n := len(data) - 1
	runs := make([]rune, 0, 256)
	for i := start; i < n; {
		u := uint16(data[i])<<8 | uint16(data[i+1])
		cls := utf16Class(u)
		if cls == cNone || cls == cLead {
			i += 2
			continue
		}
		st := runStats{r: runs[:0]}
		j := i
		for j < n {
			u := uint16(data[j])<<8 | uint16(data[j+1])
			k := utf16Class(u)
			if k == cNone || k == cLead {
				break
			}
			st.add(rune(u))
			j += 2
		}
		runs = st.r
		term := j < n && data[j] == 0 && data[j+1] == 0
		if (!opt.NeedNUL || term) && st.ok(opt, EncUTF16) {
			var s strings.Builder
			for k := i; k < j; k += 2 {
				r := rune(data[k])<<8 | rune(data[k+1])
				if r == '\\' {
					s.WriteString(`\\`)
				} else {
					var b [4]byte
					s.Write(b[:utf8.EncodeRune(b[:], r)])
				}
			}
			out = append(out, String{Addr: base + uint32(i), Size: j - i, Enc: EncUTF16,
				Text: s.String(), Chars: st.chars, Terminated: term})
			if opt.Max > 0 && len(out) >= opt.Max {
				return out
			}
		}
		i = j
	}
	return out
}

func mergeByAddr(a, b []String) []String {
	if len(b) == 0 {
		return a
	}
	out := make([]String, 0, len(a)+len(b))
	i, j := 0, 0
	for i < len(a) || j < len(b) {
		if j >= len(b) || i < len(a) && a[i].Addr <= b[j].Addr {
			out = append(out, a[i])
			i++
		} else {
			out = append(out, b[j])
			j++
		}
	}
	return out
}
