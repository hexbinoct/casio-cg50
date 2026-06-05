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
  0xA4080000  KEYSC keyboard matrix controller (12 data regs +0..0x16)
  0xA4490000  TMU  timer unit ; 0xA44A0000 ETMU
  0xA4610000  timer/periph IRQ block (ack @ +0x88)
  0xFEC10000  bus/SDRAM controller (16-reg timing block)
  0xFE008000  DMAC (ch regs; ch2 @ +0x20; DMAOR @ +0x60)
  0xFF000000  CCN/MMU/cache (PTEH/PTEL/TTB/TEA/MMUCR/CCR/INTEVT/EXPEVT...)
  0xB4000000  R61524 LCD (area5; cmd @ +0, data @ +2)
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


class KEYSC(Region):
    """Keyboard matrix controller. All-keys-released = 0 in every data register."""
    def read(self, va, size):
        return 0


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
    def read(self, va, size):
        off = (va - self.base) & 0xFFFF
        if off == 0xD8:
            cyc = self.bus.cpu.cycles if (self.bus and self.bus.cpu) else 0
            return (-(cyc >> 2)) & 0xFFFFFF     # down-counter, ~1 per 4 instr
        return self.regs.get(off, 0)


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
        self.regions = [
            CPG("CPG", 0xA4150000, 0x1000),
            Region("PFC", 0xA4050000, 0x1000),
            Region("WDT", 0xA4520000, 0x1000),
            KEYSC("KEYSC", 0xA4080000, 0x1000),
            Region("TMU", 0xA4490000, 0x1000),
            ETMU("ETMU", 0xA44A0000, 0x1000),
            self.etmu2,
            self.periph_irq,
            FreeCounter("FRC", 0xA4130000, 0x10000),   # free-running counter (delay loops)
            INTX("INTX", 0xA4140000, 0x1000),
            KEYSC("KIU_DATA", 0xA44B0000, 0x1000),      # SH7724-style key input data (all 0 = no key)
            BCDALU("BCDALU", 0xA4CB0000, 0x1000),        # HW packed-BCD add/sub unit (number formatting)
            Region("BSC", 0xFEC10000, 0x1000),
            DMAC("DMAC", 0xFE008000, 0x1000),
            CCN("CCN", 0xFF000000, 0x1000),        # MMU/cache/INTEVT/EXPEVT + model strap @+0x24
            Region("LCD_R61524", 0xB4000000, 0x20000),
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
        if not self.timer_period:
            return
        if cpu.cycles >= self.timer_next:
            self.timer_next = cpu.cycles + self.timer_period
            self.timer_ticks += 1
            self.periph_irq.set_timer_flag()
            cpu.raise_irq(self.TIMER_INTEVT, self.TIMER_LEVEL)

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
    if "KIU_DATA" not in names:
        mmio.regions.insert(0, KEYSC("KIU_DATA", 0xA44B0000, 0x1000))
    if "BCDALU" not in names:
        mmio.regions.insert(0, BCDALU("BCDALU", 0xA4CB0000, 0x1000))
