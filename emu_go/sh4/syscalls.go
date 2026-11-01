package sh4

import "sort"

// SyscallName is one entry of the built-in syscall name list.
type SyscallName struct {
	Num  uint16
	Name string
}

// SyscallTrampoline is the OS entry every syscall goes through: an add-in loads the number in
// r0 and jumps here; its first literal load is the address of the syscall table.
const SyscallTrampoline = 0x80020070

// SyscallTableLen is how many table entries are read (DASM's bound; the 3.60 table has 8,040,
// 0x0000-0x1F67).
const SyscallTableLen = 0x2000

// SyscallNames is the built-in list of 273 names (from libfxcg), sorted by number.
func SyscallNames() []SyscallName { return syscallNames[:] }

// SyscallNameOf returns the name of syscall num, "" when the list has none.
func SyscallNameOf(num uint32) string {
	i := sort.Search(len(syscallNames), func(i int) bool { return uint32(syscallNames[i].Num) >= num })
	if i < len(syscallNames) && uint32(syscallNames[i].Num) == num {
		return syscallNames[i].Name
	}
	return ""
}

// SyscallByName returns the number of the syscall called name (exact, case-sensitive).
func SyscallByName(name string) (uint32, bool) {
	for _, s := range syscallNames {
		if s.Name == name {
			return uint32(s.Num), true
		}
	}
	return 0, false
}

func romAddr(v uint32) bool { return v>>24 == 0x80 || v>>24 == 0xA0 }

type addrNum struct{ addr, num uint32 }

// Syscalls is the syscall table of an OS image: number -> handler address, and the handler
// names that follow from it, so they fit whatever OS version the image is.
type Syscalls struct {
	Base  uint32    // the table's address
	img   *Image    // the OS image the table was read from
	named []addrNum // handlers of the named syscalls, sorted by address (then number)
	all   []addrNum // every entry whose handler is a ROM address, sorted the same way
}

// FindSyscalls reads the syscall table of an OS image (mapped at 0x80000000): the trampoline's
// mov.l @(disp,pc),r2 loads the table base, as in DASM. Nil when there is none.
func FindSyscalls(img *Image) *Syscalls {
	var base uint32
	for a := uint32(SyscallTrampoline); a < SyscallTrampoline+0x10; a += 2 {
		op, ok := img.Word(a)
		if !ok {
			return nil
		}
		if op&0xFF00 == 0xD200 {
			t, ok := img.Long(a&^3 + 4 + uint32(op&0xFF)*4)
			if ok && romAddr(t) {
				base = t
			}
			break
		}
	}
	if base == 0 {
		return nil
	}
	s := &Syscalls{Base: base, img: img}
	for num := uint32(0); num < SyscallTableLen; num++ {
		if h, ok := img.Long(base + num*4); ok && romAddr(h) {
			s.all = append(s.all, addrNum{h, num})
		}
	}
	for _, n := range syscallNames {
		if h, ok := img.Long(base + uint32(n.Num)*4); ok && romAddr(h) {
			s.named = append(s.named, addrNum{h, uint32(n.Num)})
		}
	}
	less := func(x []addrNum) func(i, j int) bool {
		return func(i, j int) bool {
			if x[i].addr != x[j].addr {
				return x[i].addr < x[j].addr
			}
			return x[i].num < x[j].num
		}
	}
	sort.Slice(s.all, less(s.all))
	sort.Slice(s.named, less(s.named))
	return s
}

// Handler returns the table entry of syscall num (ok = readable; any value, as DASM uses it).
func (s *Syscalls) Handler(num uint32) (uint32, bool) {
	if s == nil {
		return 0, false
	}
	return s.img.Long(s.Base + num*4)
}

// Handlers returns the ROM handler of every syscall below SyscallTableLen, indexed by number
// (0 where the entry is no ROM address).
func (s *Syscalls) Handlers() []uint32 {
	if s == nil {
		return nil
	}
	h := make([]uint32, SyscallTableLen)
	for _, e := range s.all {
		h[e.num] = e.addr
	}
	return h
}

func lookup(x []addrNum, addr uint32) (uint32, bool) {
	i := sort.Search(len(x), func(i int) bool { return x[i].addr >= addr })
	if i < len(x) && x[i].addr == addr {
		return x[i].num, true
	}
	return 0, false
}

// Name returns the name of the syscall whose handler is at addr ("" if none): when several
// named numbers share a handler, the lowest number's name, as in DASM.
func (s *Syscalls) Name(addr uint32) string {
	if s == nil {
		return ""
	}
	if num, ok := lookup(s.named, addr); ok {
		return SyscallNameOf(num)
	}
	return ""
}

// Number returns the lowest syscall number whose handler is at addr, named or not.
func (s *Syscalls) Number(addr uint32) (uint32, bool) {
	if s == nil {
		return 0, false
	}
	return lookup(s.all, addr)
}

// Count is the number of named syscalls whose handler was found (DASM's names_count).
func (s *Syscalls) Count() int {
	if s == nil {
		return 0
	}
	return len(s.named)
}

// Len is the number of table entries below SyscallTableLen that hold a ROM address.
func (s *Syscalls) Len() int {
	if s == nil {
		return 0
	}
	return len(s.all)
}
