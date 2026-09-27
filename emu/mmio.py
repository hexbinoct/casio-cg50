#!/usr/bin/env python3
"""
fx-CG50 / SH7305 MMIO bus + peripheral stubs.

Goal for the skeleton: let the boot sequence PROGRESS. We don't model timing yet;
we just return values that satisfy the OS's poll loops (e.g. "PLL locked",
"DMA done") and log every access so we can compare against the RE notes.

Register identities reverse-engineered in RECON_NOTES.md:
  0xA4150000  CPG  (FRQCR; PLL-ready bit0 @ +0x60; +0x20/30/38 in reset)
  0xA4050000  PFC  (pin function / ports; 0xA4050138 strobed each main loop)
  0xA4520000  WDT  (0x5A00/0xA5xx key writes)
  0xA4080000  INTC  interrupt controller (IPR/IMR/IMCR) — NOT the keyboard
  0xA44B0000  KEYSC/KIU key-scan unit (6 key-data words + ctrl; INTEVT 0xBE0) — see KeyScan
  0xA4490000  TMU  timer unit ; 0xA44A0000 ETMU
  0xA4610000  timer/periph IRQ block (ack @ +0x88)
  0xFEC10000  bus/SDRAM controller (16-reg timing block)
  0xFE008000  DMAC (ch regs; ch2 @ +0x20; DMAOR @ +0x60)
  0xFF000000  CCN/MMU/cache (PTEH/PTEL/TTB/TEA/MMUCR/CCR/INTEVT/EXPEVT...)
  0xB4000000  R61524 LCD (area5; index/data both @ +0, RS = PFC 0xA405013C bit4) — see LCD
"""


class Region:
    """A simple logged register window with optional read hook."""
    def __init__(self, name, base, size):
        self.name = name
        self.base = base
        self.size = size
        self.regs = {}          # offset -> value (last written)

    def contains(self, va):
        return self.base <= va < self.base + self.size

    def read(self, va, size):
        return self.regs.get(va - self.base, 0)

    def write(self, va, size, val):
        self.regs[va - self.base] = val


class CCN(Region):
    """MMU/cache control. Reset stub reads the HW model strap at +0x24 (0xFF000024):
    low16==0x0000->0xCA00, ==0x0020->0xCA01, ==0x0A02->0xCA02 (fx-CG50). Report 0x0A02 so
    the OS identifies as fx-CG50 (else fls0_init's verify loop @0x80365418 never exits)."""
    def read(self, va, size):
        off = (va - self.base) & 0xFFFF
        if off == 0x24:
            return 0x0A02
        return self.regs.get(va - self.base, 0)


class CPG(Region):
    """Clock generator. The boot PLL routine spins on a 'ready' bit0 at +0x60."""
    def read(self, va, size):
        off = va - self.base
        if off == 0x60:
            return 0x0           # bit0 == 0  -> 'while (reg & 1)' exits immediately
        return self.regs.get(off, 0)


class DMAC(Region):
    """DMA controller. Bdisp_PutDisp_DD spins on CHCR (ch base +0x0C... here ch2
    block at +0x20, control word index [3]) until the 'transfer end' (TE) bit set.
    We complete instantly: report TE=1 (bit1) so the wait loop exits."""
    def read(self, va, size):
        off = va - self.base
        # CHCR of every channel sits at base+0xC (channels spaced 0x10). The OS waits
        # on bit1 (TE, transfer end). We complete DMA instantly -> always report TE.
        if (off & 0xF) == 0xC:
            return self.regs.get(off, 0) | 0x2
        return self.regs.get(off, 0)


class INTCStub(Region):
    """INTC (0xA4080000, SH7724-style IPR/IMR/IMCR) — NOT the keyboard (cont.18l). Masking
    isn't modelled (CPU IMASK/BL gating suffices); reads return 0 as they always did."""
    def read(self, va, size):
        return 0


class KeyScan(Region):
    """SH7305 key-scan unit (KIU/KEYSC) @0xA44B0000 — the real path a keypress takes into
    the OS. Exact mirror of emu_go/keysc.go (see its header for the RE'd register map):
      +0x00..+0x0B six key-data words: word = col>>1, bit = row + 8*(col&1) (0-based grid
                   of re/KEYMAP.md); +0x0C ctrl (bit15 enable); +0x10 mode (0x200 normal,
                   0x400 detect-only, 0x800 scan-now, 0 off); +0x12 busy(=0);
      +0x14 [15:8] IRQ enable per flag, [7:0] flags W1C: bit3 key-detect, bit1 scan-complete.
    Every scan_period cycles: press edge -> detect flag; while held (+2 scans after
    release) -> scan-complete flag; mode 0x800 -> scan-complete every period. IRQ INTEVT
    0xBE0 level 13 while (flags & enable) != 0."""
    INTEVT = 0xBE0
    LEVEL = 13
    FLAG_SCAN = 0x02
    FLAG_DETECT = 0x08
    DEFAULT_SCAN_PERIOD = 750000    # 30.3 ms/scan measured on the real calc (tools/keyprobe 2026-09-27)

    def __init__(self, name, base, size, scan_period=DEFAULT_SCAN_PERIOD):
        super().__init__(name, base, size)
        self.held = [0] * 6
        self.ctrl = self.mode = self.ie = self.flags = 0
        self.scan_period = scan_period
        self.scan_next = 0
        self.scan_left = 0
        self.was_held = False

    def any_held(self):
        return any(self.held)

    def press(self, row, col):
        if row < 8 and col < 12:
            if not self.any_held():
                self.scan_next = 0      # key-detect is edge-triggered: scan at the next tick
            self.held[col >> 1] |= 1 << (row + 8 * (col & 1))

    def release(self, row, col):
        if row < 8 and col < 12:
            self.held[col >> 1] &= ~(1 << (row + 8 * (col & 1))) & 0xFFFF

    def release_all(self):
        self.held = [0] * 6

    def resume_defaults(self):
        self.ctrl, self.mode, self.ie, self.flags = 0x8000, 0x200, 0x48, 0
        self.scan_left, self.was_held = 0, False

    def read(self, va, size):
        off = va - self.base
        if off < 0x0C:
            w = self.held[off >> 1]
            if size == 2:
                return w
            if size == 1:
                return (w >> 8) if (off & 1) == 0 else (w & 0xFF)
            lo = self.held[(off >> 1) + 1] if (off >> 1) + 1 < 6 else 0
            return (w << 16) | lo
        if off == 0x0C:
            return self.ctrl
        if off == 0x10:
            return self.mode
        if off == 0x12:
            return 0
        if off == 0x14:
            return (self.ie << 8) | self.flags
        return self.regs.get(off, 0)

    def write(self, va, size, val):
        off = va - self.base
        if off == 0x0C:
            self.ctrl = val & 0xFFFF
        elif off == 0x10:
            self.mode = val & 0xFFFF
        elif off == 0x14:
            self.ie = (val >> 8) & 0xFF
            self.flags &= ~(val & 0xFF) & 0xFF
        else:
            self.regs[off] = val

    def tick(self, cpu):
        if cpu.cycles < self.scan_next:
            return
        self.scan_next = cpu.cycles + self.scan_period
        held = self.any_held()
        if (self.ctrl & 0x8000) == 0 or self.mode == 0:
            self.was_held = held
            return
        if held and not self.was_held:
            self.flags |= self.FLAG_DETECT
        if held:
            self.scan_left = 2
        if (self.mode & 0x800) or ((self.mode & 0x200) and (held or self.scan_left > 0)):
            self.flags |= self.FLAG_SCAN
            if not held and self.scan_left > 0:
                self.scan_left -= 1
        self.was_held = held
        if self.flags & self.ie:
            cpu.raise_irq(self.INTEVT, self.LEVEL)


class ETMU(Region):
    """Extra timer unit (0xA44A0000). Boot/init does one-shot delays: set start bit
    at +0, poll the elapsed/underflow flag (bit15) at +0x60. We have no real time
    model yet, so report 'elapsed' immediately so the wait completes."""
    def read(self, va, size):
        off = (va - self.base) & 0xFFFF
        if off == 0x60:
            return 0x8000          # timer-elapsed flag set
        return self.regs.get(off, 0)


class FreeCounter(Region):
    """A free-running counter peripheral (0xA4130000 area). Boot delay loops read
    it twice and wait for the value to advance, so it must increment on every read.
    Models a monotonically-rising timer/RTC sub-counter; refine identity later."""
    def __init__(self, name, base, size):
        super().__init__(name, base, size)
        self.count = 0

    def read(self, va, size):
        self.count = (self.count + 1) & 0xFFFFFFFF
        return self.count & ((1 << (size * 8)) - 1)


# Battery-voltage ADC reading reported at PERIPH_IRQ +0x82/+0x84. The OS battery
# monitor (FUN_801de54a) averages two samples and buckets the result (>>6) against
# thresholds ~347-475 (FUN_801e6bbc). A 0 reading falls below all thresholds -> level
# 0x12 -> the shell raises a "battery event" every loop and SKIPS drawing the main
# menu, parking in the idle event-pump. Reporting a normal mid-range voltage
# (raw>>6 ~= 453 -> bucket 2 "normal") lets the menu draw. 0x7140>>6 = 453.
ADC_BATTERY_RAW = 0x7140

class PeriphIRQ(Region):
    """0xA4610000 timer/peripheral interrupt block. The OS idle loop polls the flag
    register at +0x88 (bits 14/15 = timer underflow) and the timer ISR (INTEVT 0x188)
    acks it by clearing those bits. We let the run-loop's timer set the flag; the ISR's
    write-back clears it. Flag lives at offset 0x88 (treat the 0x88..0x8B word as one).
    +0x82/+0x84 are the battery-voltage ADC data registers (see ADC_BATTERY_RAW)."""
    def read(self, va, size):
        roff = (va - self.base) & 0xFFFF
        if roff == 0x82 or roff == 0x84:
            return ADC_BATTERY_RAW
        off = (va - self.base) & ~3 if (0x88 <= (va - self.base) < 0x8C) else (va - self.base)
        return self.regs.get(off, 0)

    def write(self, va, size, val):
        off = (va - self.base) & ~3 if (0x88 <= (va - self.base) < 0x8C) else (va - self.base)
        self.regs[off] = val

    def set_timer_flag(self):
        self.regs[0x88] = self.regs.get(0x88, 0) | 0xC000   # bits 14,15


class ETMUCounter(Region):
    """ETMU extra-timer block (0xA44D0000). The OS uses the down-counter at +0xD8 as a
    fine-grained delay reference: it reads it once, then spins reading it until
    (reference - current) >= N. A real TCNT decrements at the peripheral clock, so we
    return a value that DECREASES with cpu.cycles (via the bus back-ref). Other offsets
    (control/TCOR/TSTR) are stored/echoed. Reads of 0xD8 by a stuck-at-0 stub deadlock
    the delay loop forever; a moving counter lets it complete."""
    def __init__(self, name, base, size):
        super().__init__(name, base, size)
        self.bus = None
        # instructions per counter tick: the real counter (+0xD8, aliased +0xC8) is a 32-bit
        # down-counter at 32.768 kHz (measured: tools/timerprobe 2026-09-27); hosts derive
        # count_div = instr_per_sec/32768. 0 keeps the legacy every-4-instr 24-bit behaviour.
        self.count_div = 0
    def read(self, va, size):
        off = (va - self.base) & 0xFFFF
        if off in (0xD8, 0xC8):
            cyc = self.bus.cpu.cycles if (self.bus and self.bus.cpu) else 0
            if not self.count_div:
                return (-(cyc >> 2)) & 0xFFFFFF     # down-counter, ~1 per 4 instr
            return (-(cyc // self.count_div)) & 0xFFFFFFFF
        return self.regs.get(off, 0)


class RTC(Region):
    """SH7305 RTC @0xA413FEC0 - exact mirror of emu_go/rtc.go (see its header for the RE).
    +0x00 R64CNT (bits 6..0 = 1..64 Hz counter, bit0 toggles at 128 Hz); +0x02..+0x0E BCD
    sec/min/hour/weekday/day/month/year(16-bit); +0x1C RCR1; +0x1E RCR2 (PEF bit7, PES bits
    6..4: 1=1/256 s .. 7=2 s). Each period (phase-aligned to the RTC clock): PEF set + INTEVT
    0xAA0 level 9. The OS idle path arms PES=1/2 s before `sleep` = cursor blink / heartbeat."""
    INTEVT = 0xAA0
    LEVEL = 9
    EPOCH_DOW = 5     # 2010-01-01 was a Friday
    EPOCH_2010 = 1262304000

    def __init__(self, name, base, size):
        super().__init__(name, base, size)
        self.bus = None
        self.rcr1 = 0
        self.rcr2 = 0
        self.epoch_sec = 0
        self.next_periodic = 0

    def _ips(self):
        return self.bus.instr_per_sec if (self.bus and self.bus.instr_per_sec) else 70_000_000

    def _cycles(self):
        return self.bus.cpu.cycles if (self.bus and self.bus.cpu) else 0

    def set_clock(self, unix):
        s = max(0, unix - self.EPOCH_2010)
        self.epoch_sec = s - self._cycles() // self._ips()

    def period_instr(self):
        ips = self._ips()
        pes = (self.rcr2 >> 4) & 7
        return {1: ips // 256, 2: ips // 64, 3: ips // 16, 4: ips // 4,
                5: ips // 2, 6: ips, 7: ips * 2}.get(pes, 0)

    def align_periodic(self):
        p = self.period_instr()
        self.next_periodic = 0 if p == 0 else (self._cycles() // p + 1) * p

    @staticmethod
    def _bcd(v):
        return ((v // 10) << 4) | (v % 10)

    def read(self, va, size):
        off = va - self.base
        cyc, ips = self._cycles(), self._ips()
        sec = self.epoch_sec + cyc // ips
        if off == 0x00:
            return ((cyc % ips) * 128 // ips) & 0x7F
        if off == 0x02:
            return self._bcd(sec % 60)
        if off == 0x04:
            return self._bcd(sec // 60 % 60)
        if off == 0x06:
            return self._bcd(sec // 3600 % 24)
        if off == 0x08:
            return (sec // 86400 + self.EPOCH_DOW) % 7
        if off in (0x0A, 0x0C, 0x0E):
            days = sec // 86400
            y = 2010
            while True:
                ylen = 366 if (y % 4 == 0 and (y % 100 != 0 or y % 400 == 0)) else 365
                if days < ylen:
                    break
                days -= ylen
                y += 1
            mlen = [31, 28, 31, 30, 31, 30, 31, 31, 30, 31, 30, 31]
            if y % 4 == 0 and (y % 100 != 0 or y % 400 == 0):
                mlen[1] = 29
            m = 0
            while m < 11 and days >= mlen[m]:
                days -= mlen[m]
                m += 1
            if off == 0x0A:
                return self._bcd(days + 1)
            if off == 0x0C:
                return self._bcd(m + 1)
            return (self._bcd(y // 100) << 8) | self._bcd(y % 100)
        if off == 0x1C:
            return self.rcr1
        if off == 0x1E:
            return self.rcr2
        return self.regs.get(off, 0)

    def write(self, va, size, val):
        off = va - self.base
        if off == 0x1C:
            self.rcr1 = val & 0xFF
        elif off == 0x1E:
            prev = (self.rcr2 >> 4) & 7
            self.rcr2 = val & 0xFF
            pes = (self.rcr2 >> 4) & 7
            if pes and (pes != prev or self.next_periodic == 0):
                self.align_periodic()
        else:
            self.regs[off] = val

    def tick(self, cpu):
        if not (self.rcr2 & 0x70):
            return
        if cpu.cycles >= self.next_periodic:
            self.next_periodic += self.period_instr()
            self.rcr2 |= 0x80
            cpu.raise_irq(self.INTEVT, self.LEVEL)


class INTX(Region):
    """0xA4140000 block used by the keyboard driver (regs +0x24/+0x64). The timer
    ISR's key-scan poll loop (0x801dff06) reads byte +0x24 and spins until bit6 or
    bit5 is set (a 'key-scan complete / event ready' flag from the KIU). With a 0
    stub the ISR never returns (BL stuck) -> interrupts wedge. We report scan-ready
    (bit6) so each scan completes; writes (acks) are stored but reads keep bit6 set
    so the next timer ISR's scan also completes. KIU data stays 0 = no key pressed."""
    def read(self, va, size):
        off = (va - self.base) & 0xFFFF
        if off == 0x24:
            return 0x40                      # bit6: scan complete / ready
        return self.regs.get(off, 0)


def _bcd_add(a, b, carry):
    """8-nibble packed-BCD add with carry in/out (per 32-bit word)."""
    res = 0
    for i in range(8):
        s = ((a >> (4 * i)) & 0xF) + ((b >> (4 * i)) & 0xF) + carry
        carry = 1 if s >= 10 else 0
        if s >= 10:
            s -= 10
        res |= s << (4 * i)
    return res & 0xFFFFFFFF, carry


def _bcd_sub(a, b, borrow):
    """8-nibble packed-BCD subtract with borrow in/out (per 32-bit word)."""
    res = 0
    for i in range(8):
        s = ((a >> (4 * i)) & 0xF) - ((b >> (4 * i)) & 0xF) - borrow
        borrow = 1 if s < 0 else 0
        if s < 0:
            s += 10
        res |= s << (4 * i)
    return res & 0xFFFFFFFF, borrow


class BCDALU(Region):
    """Hardware multi-word BCD arithmetic unit @0xA4CB0000 (RE'd cont.18c, command set
    confirmed by on-device probe cont.18e — see os/devic_probes/). The Casio number/format
    library drives it for every decimal +/- (FUN_80072e78 etc.); the SH4 has no BCD opcodes
    so this peripheral does the packed-BCD digit math while software handles shifts/masks
    (SHLD). Registers (the 4-word block aliases every 0x10; the OS uses the +0x10 alias):
        +0x00 command/status   +0x04 operand A   +0x08 operand B   +0x0C result
    Operands are sticky; a command write triggers the op and the result is valid immediately.
    The mantissa is processed one 32-bit word at a time, LSW first. There is a SINGLE shared
    carry/borrow latch (proven on hardware: a sub's borrow-out feeds a following add as
    carry-in). Command decode (16-bit; bit3 ignored so 8..15 mirror 0..7):
        op      = (cmd&1) ? BCD add : BCD sub
        flag_in = (cmd&4) ? 1 : (cmd&2) ? latch : 0      (forced-1 / latched / forced-0)
        flag_out = carry (add) or borrow (sub) -> latched for the next op.
    So: 0=A-B 1=A+B 2=A-B-flag 3=A+B+flag 4=A-B-1 5=A+B+1 (OS only uses 0..4).
    VALIDATED: with this model the real OS formatter renders "98765"/"4.695555556" instead
    of "0". Leaving the unit unmodelled makes the result register read 0, which is the root
    cause of "all results show 0" (cont.18c)."""
    def __init__(self, name, base, size):
        super().__init__(name, base, size)
        self.A = 0
        self.B = 0
        self.result = 0
        self.flag = 0          # shared carry/borrow latch (one bit)

    def _compute(self, cmd):
        if cmd & 4:
            fin = 1
        elif cmd & 2:
            fin = self.flag
        else:
            fin = 0
        if cmd & 1:
            self.result, self.flag = _bcd_add(self.A, self.B, fin)
        else:
            self.result, self.flag = _bcd_sub(self.A, self.B, fin)

    def write(self, va, size, val):
        off = (va - self.base) & 0xF       # block aliases every 0x10
        val &= (1 << (size * 8)) - 1
        if off == 0x4:
            self.A = val & 0xFFFFFFFF
        elif off == 0x8:
            self.B = val & 0xFFFFFFFF
        elif off == 0x0:                   # command -> compute (bit3 ignored)
            self._compute(val & 0x7)
        else:
            self.regs[(va - self.base) & 0xFFFF] = val

    def read(self, va, size):
        off = (va - self.base) & 0xF
        if off == 0xC:                     # result
            return self.result
        if off == 0x0:                     # command/status: shared flag observable (cont.18e)
            return 0x10040000 if self.flag else 0x00010000
        return self.regs.get((va - self.base) & 0xFFFF, 0)


class LCD(Region):
    """R61524 LCD controller (area 5) — CPU-visible half of emu_go/lcd.go (cont.18o).
    RS = PFC 0xA405013C bit4: low -> the write selects a register index (a read returns the
    index); high -> data read/write of the selected register. The OS read-modify-writes R003
    (entry mode) and tests its ORG bit to pick window addressing, so register read-back must
    be real. Boot leaves R003=0x00A0 and the window R210-R213 = full 396x224 panel. GRAM,
    the address counter and DMA streaming are presentation-only (Go side); a GRAM read
    (R202) returns 0 in both implementations."""
    def __init__(self, name, base, size, pfc):
        super().__init__(name, base, size)
        self.pfc = pfc
        self.idx = 0
        self.reg = {}
        self.boot_defaults()

    def boot_defaults(self):
        self.reg = {0x003: 0x00A0, 0x210: 0, 0x211: 395, 0x212: 0, 0x213: 223}

    def _rs(self):
        return bool(self.pfc.regs.get(0x13C, 0) & 0x10)

    def read(self, va, size):
        if not self._rs():
            return self.idx
        if self.idx == 0x202:
            return 0
        return self.reg.get(self.idx, 0)

    def write(self, va, size, val):
        if not self._rs():
            self.idx = val & 0x7FF
        elif self.idx != 0x202:
            self.reg[self.idx] = val & 0xFFFF


class MMIOBus:
    # Timer interrupt source. The idle OS polls PERIPH_IRQ 0xA4610088 (bits 14/15) and
    # waits on INTEVT 0x560 -> handler 0x801ded94, which acks those bits. (0x188 from the
    # old RECON notes was wrong: the dispatcher indexes a 4-byte table by (INTEVT-0x40)>>3,
    # so a valid INTEVT must be a multiple of 0x20; 0x560 is the one whose ISR clears 14/15.
    # Verified empirically in emu/test_candidates.py against the live 3.60 handler table.)
    TIMER_INTEVT = 0x560
    TIMER_LEVEL  = 8

    def __init__(self, log=True):
        self.log = log
        self.cpu = None         # set by the runner; used by cycle-based timers
        self.periph_irq = PeriphIRQ("PERIPH_IRQ", 0xA4610000, 0x1000)
        self.etmu2 = ETMUCounter("ETMU2", 0xA44D0000, 0x1000)
        self.etmu2.bus = self
        self.keysc = KeyScan("KEYSC", 0xA44B0000, 0x1000)
        self.rtc = RTC("RTC", 0xA413FEC0, 0x40)
        self.rtc.bus = self
        self.instr_per_sec = 70_000_000   # host throughput; converts cycles to time (RTC/ETMU/KEYSC)
        pfc = Region("PFC", 0xA4050000, 0x1000)
        self.lcd = LCD("LCD_R61524", 0xB4000000, 0x20000, pfc)
        self.regions = [
            CPG("CPG", 0xA4150000, 0x1000),
            pfc,
            Region("WDT", 0xA4520000, 0x1000),
            INTCStub("INTC", 0xA4080000, 0x1000),       # interrupt controller (NOT keyboard)
            Region("TMU", 0xA4490000, 0x1000),
            ETMU("ETMU", 0xA44A0000, 0x1000),
            self.etmu2,
            self.periph_irq,
            self.rtc,                                    # listed before FRC: same page, must win
            FreeCounter("FRC", 0xA4130000, 0x10000),   # free-running counter (delay loops)
            INTX("INTX", 0xA4140000, 0x1000),
            self.keysc,                                  # key-scan unit = the real key path
            BCDALU("BCDALU", 0xA4CB0000, 0x1000),        # HW packed-BCD add/sub unit (number formatting)
            Region("BSC", 0xFEC10000, 0x1000),
            DMAC("DMAC", 0xFE008000, 0x1000),
            CCN("CCN", 0xFF000000, 0x1000),        # MMU/cache/INTEVT/EXPEVT + model strap @+0x24
            self.lcd,
        ]
        self.unknown = {}       # va -> count, for unmapped MMIO
        # interrupt-timer state (cycle-based proxy for real time)
        self.timer_period = 0   # 0 = disabled; set by the runner to enable ticks
        self.timer_next = 0
        self.timer_ticks = 0

    def tick(self, cpu):
        """Cycle-driven timer: every `timer_period` instructions, set the PERIPH_IRQ
        flag and request INTEVT 0x560. Safe to free-run from boot — cpu._accept_interrupt
        gates on SR.BL/IMASK, so the OS only takes it once its vectors are set up."""
        self.keysc.tick(cpu)
        if not self.timer_period:
            return          # pure-boot mode (goldens): no interrupt sources at all
        self.rtc.tick(cpu)
        if cpu.cycles >= self.timer_next:
            self.timer_next = cpu.cycles + self.timer_period
            self.timer_ticks += 1
            self.periph_irq.set_timer_flag()
            cpu.raise_irq(self.TIMER_INTEVT, self.TIMER_LEVEL)

    def set_instr_per_second(self, ips):
        """Mirror of MMIOBus.SetInstrPerSecond: anchors KEYSC scan (33 Hz), the 32.768 kHz
        counter and the RTC to the host's real throughput."""
        ips = max(1_000_000, int(ips))
        self.instr_per_sec = ips
        self.keysc.scan_period = ips // 33
        self.etmu2.count_div = ips // 32768
        if self.rtc.rcr2 & 0x70:
            self.rtc.align_periodic()

    def _find(self, va):
        # collapse P0/P1/P2 mirrors so 0x14000000 / 0xB4000000 both hit the LCD etc.
        for cand in (va, (va & 0x1FFFFFFF) | 0xA0000000, (va & 0x1FFFFFFF)):
            for r in self.regions:
                if r.contains(cand):
                    return r, cand
        return None, va

    def read(self, va, size):
        r, hit = self._find(va)
        if r is None:
            self.unknown[va] = self.unknown.get(va, 0) + 1
            if self.log:
                print(f"  [mmio] rd{size*8} ???        0x{va:08x} -> 0")
            return 0
        val = r.read(hit, size)
        if self.log:
            print(f"  [mmio] rd{size*8} {r.name:12s} 0x{va:08x} -> 0x{val:0{size*2}x}")
        return val

    def write(self, va, size, val):
        r, hit = self._find(va)
        if r is None:
            self.unknown[va] = self.unknown.get(va, 0) + 1
            if self.log:
                print(f"  [mmio] wr{size*8} ???        0x{va:08x} <- 0x{val:0{size*2}x}")
            return
        r.write(hit, size, val)
        if self.log:
            print(f"  [mmio] wr{size*8} {r.name:12s} 0x{va:08x} <- 0x{val:0{size*2}x}")


def upgrade_bus(mmio, cpu):
    """Make a snapshot-loaded (older) MMIOBus current: wire the cpu ref and add/replace
    peripherals introduced after the snapshot was saved (ETMU2 counter, INTX scan-ready,
    KIU data). Idempotent — safe to call on a fresh or already-upgraded bus."""
    mmio.cpu = cpu
    names = {getattr(r, "name", "") for r in mmio.regions}
    if "ETMU2" not in names:
        e = ETMUCounter("ETMU2", 0xA44D0000, 0x1000); e.bus = mmio
        mmio.etmu2 = e; mmio.regions.insert(0, e)
    else:
        mmio.etmu2.bus = mmio
    mmio.regions = [r for r in mmio.regions if getattr(r, "name", "") != "INTX"]
    mmio.regions.insert(0, INTX("INTX", 0xA4140000, 0x1000))
    mmio.regions = [r for r in mmio.regions if getattr(r, "name", "") not in ("KIU_DATA", "KEYSC")]
    if not isinstance(getattr(mmio, "keysc", None), KeyScan):
        mmio.keysc = KeyScan("KEYSC", 0xA44B0000, 0x1000)
    mmio.regions.insert(0, mmio.keysc)
    mmio.regions = [r for r in mmio.regions if getattr(r, "name", "") != "INTC"]
    mmio.regions.insert(0, INTCStub("INTC", 0xA4080000, 0x1000))
    if not isinstance(getattr(mmio, "rtc", None), RTC):
        mmio.rtc = RTC("RTC", 0xA413FEC0, 0x40)
        mmio.rtc.bus = mmio
        mmio.regions = [r for r in mmio.regions if getattr(r, "name", "") != "RTC"]
        mmio.regions.insert(0, mmio.rtc)
    if not hasattr(mmio, "instr_per_sec"):
        mmio.instr_per_sec = 70_000_000
    if not hasattr(mmio.etmu2, "count_div"):
        mmio.etmu2.count_div = 0
    if "BCDALU" not in names:
        mmio.regions.insert(0, BCDALU("BCDALU", 0xA4CB0000, 0x1000))
    if not isinstance(getattr(mmio, "lcd", None), LCD):
        pfc = next(r for r in mmio.regions if getattr(r, "name", "") == "PFC")
        mmio.lcd = LCD("LCD_R61524", 0xB4000000, 0x20000, pfc)
        mmio.regions = [r for r in mmio.regions if getattr(r, "name", "") != "LCD_R61524"]
        mmio.regions.append(mmio.lcd)
