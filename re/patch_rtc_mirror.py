"""Mirror the RTC, the 32.768 kHz counter divisor and the host time base into the Python
oracle (emu/mmio.py). Exact-string edits with assertions; skips edits already applied."""
p = "F:/ru/myprojects/may/cg50/emu/mmio.py"
s = open(p, encoding="utf-8", newline="").read()


def rep(old, new):
    global s
    if new in s and old not in s:
        print("  [skip] already applied:", old[:40].strip())
        return
    assert s.count(old) == 1, old[:60]
    s = s.replace(old, new)


rep("""    def __init__(self, name, base, size):
        super().__init__(name, base, size)
        self.bus = None
    def read(self, va, size):
        off = (va - self.base) & 0xFFFF
        if off == 0xD8:
            cyc = self.bus.cpu.cycles if (self.bus and self.bus.cpu) else 0
            return (-(cyc >> 2)) & 0xFFFFFF     # down-counter, ~1 per 4 instr
        return self.regs.get(off, 0)
""", """    def __init__(self, name, base, size):
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
""")

RTC_CLASS = '''class RTC(Region):
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
'''
rep("class INTX(Region):\n", RTC_CLASS)

rep('        self.keysc = KeyScan("KEYSC", 0xA44B0000, 0x1000)\n',
    '        self.keysc = KeyScan("KEYSC", 0xA44B0000, 0x1000)\n'
    '        self.rtc = RTC("RTC", 0xA413FEC0, 0x40)\n'
    '        self.rtc.bus = self\n'
    '        self.instr_per_sec = 70_000_000   # host throughput; converts cycles to time (RTC/ETMU/KEYSC)\n')
rep('            FreeCounter("FRC", 0xA4130000, 0x10000),   # free-running counter (delay loops)\n',
    '            self.rtc,                                    # listed before FRC: same page, must win\n'
    '            FreeCounter("FRC", 0xA4130000, 0x10000),   # free-running counter (delay loops)\n')
rep("""        self.keysc.tick(cpu)
        if not self.timer_period:
            return
        if cpu.cycles >= self.timer_next:""", """        self.keysc.tick(cpu)
        if not self.timer_period:
            return          # pure-boot mode (goldens): no interrupt sources at all
        self.rtc.tick(cpu)
        if cpu.cycles >= self.timer_next:""")
rep("    def _find(self, va):\n", '''    def set_instr_per_second(self, ips):
        """Mirror of MMIOBus.SetInstrPerSecond: anchors KEYSC scan (33 Hz), the 32.768 kHz
        counter and the RTC to the host's real throughput."""
        ips = max(1_000_000, int(ips))
        self.instr_per_sec = ips
        self.keysc.scan_period = ips // 33
        self.etmu2.count_div = ips // 32768
        if self.rtc.rcr2 & 0x70:
            self.rtc.align_periodic()

    def _find(self, va):
''')
rep("""    mmio.regions = [r for r in mmio.regions if getattr(r, "name", "") != "INTC"]
    mmio.regions.insert(0, INTCStub("INTC", 0xA4080000, 0x1000))
""", """    mmio.regions = [r for r in mmio.regions if getattr(r, "name", "") != "INTC"]
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
""")
open(p, "w", encoding="utf-8", newline="").write(s)
print("mmio.py mirrored")
