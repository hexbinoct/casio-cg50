# ClassPad features missing from the fx-CG50, and which an add-in can supply

Research note, 2026-10-11. Question: what does the Casio ClassPad II (fx-CP400 / fx-CG500)
do that the fx-CG50 does not, and how much of it could third-party add-ins bring to the CG50
(and so to our emulator, which runs every add-in)?

Short version: **the two calculators are the same silicon** (Renesas SH7305, same CPU family,
both with 500 KB of user RAM advertised); the ClassPad differs in software (the CAS) and in
hardware we cannot add (touch screen, 320x528 display). Almost every *software* gap is already
closed by one existing add-in, KhiCAS, and the remaining gaps are small, numeric apps that are
easy to write. The one thing no add-in can change is **exam legality: Examination Mode disables
all add-ins**, so a CAS add-in is only ever a study tool.

## 1. What the ClassPad has that the CG50 lacks

Sources: Casio's ClassPad II feature page, the fx-CP400 user's guide, Casio's CG50 add-in and
exam pages, and the repo's own memory map. Items are grouped by how an add-in could address them.

### 1a. Software gaps an add-in CAN close

| ClassPad feature | CG50 built-in status | Add-in situation |
|---|---|---|
| **Computer algebra**: expand, factor, symbolic solve, symbolic diff/integrate, limits, Taylor, Σ/∏, exact arithmetic | None. Casio's own exam listing says the CG50 "does not have symbolic algebra manipulation or symbolic differentiation or integration". Run-Matrix only solves numerically (SolveN) | **Already exists: KhiCAS** (Giac/Xcas port by Bernard Parisse, GPL2) and **Eigenmath** (gbl08ma's Prizm port, separate CG50 build by Parisse). KhiCAS covers all of this and more |
| **Laplace / Fourier transforms**, Dirac delta, Heaviside step, Gamma | None | KhiCAS: Giac has `laplace`, `invlaplace`, `fourier` and the special functions |
| **Differential Equation Graph** app: vector/slope fields, solution curves for 1st, 2nd and n-th order ODEs | None at all. Equation mode only does polynomials and simultaneous linear systems | KhiCAS has `desolve`, `odesolve`, `plotfield`, `plotode`, but in a text UI. Casio shipped a Slope Field Graph Python script for the Math+ line that TI-Planet adapted to the CG50 Python. **A dedicated, friendly DiffEq-Graph add-in is the clearest unfilled niche** (see §3) |
| **CAS-aware Spreadsheet** (symbolic cells, CellIf) | CG50 Spreadsheet is numeric only, 26 x 999 | KhiCAS's full version includes a CAS spreadsheet |
| **CAS-linked Geometry** (drop a figure into Main to get its equation; conics by focus) | CG50 Geometry add-in is dynamic and animated but purely numeric | KhiCAS full version has interactive 2D and 3D geometry commands (no stylus, keyboard driven) |
| **Interactive manipulation** of equations (apply an operation to both sides, step by step), "Algebra Assistant" | None | Needs a CAS underneath. Doable as a mode on top of Giac, nobody has built it |
| **Verify** (checks each algebraic step for equivalence) and **Probability** sub-apps | None | Trivial given a CAS (Verify = `simplify(lhs-rhs)==0`); Probability is numeric. Both are small add-in or Python candidates |
| **Sequence** app with explicit *and* recursive definitions, sequence-type graphs | CG50 Recursion app covers recursive sequences; explicit ones go through Table | Minor gap; a thin add-in or Python script |
| **Piecewise and user-defined functions** as first-class objects | Python only | KhiCAS has `piecewise` and `Define` |
| Arbitrary parametric **3D surfaces** | CG50 3D Graph add-in does z=f(x,y), parametric, rotation bodies and templates; the AU variant drops line/plane intersections | Mostly covered already; KhiCAS adds a (slow) 3D/4D engine |
| ClassPad **programming language** extras (Locate, Print, dialogs) | CG50 has Casio BASIC *and* Python; the ClassPad has **no Python** | CG50 is ahead here. PythonExtra (MicroPython 1.23, gint graphics) widens the lead |

### 1b. Hardware gaps an add-in CANNOT close

- **Touch screen and stylus**: drag and drop, split screen operated by finger, soft keyboards.
  The CG50 has no digitizer. The best an add-in can do is keyboard-driven two-pane layouts.
- **Display**: 320 x 528 (4.8 in, portrait or horizontal view) versus 384 x 216. The CG50 has
  fewer pixels in total (82 944 vs 168 960) and no portrait mode. Any ClassPad-style app has to be
  redesigned for a short, wide screen.
- **Flash**: ClassPad II has 24 MB of flash with 5.5 MB reserved for eActivity; the CG50 has
  16 MB user storage (less on the AU model). The 19.5 MB `fls0` region holds add-ins; the
  OS maps at most about 2 MB of a single `.g3a` (KhiCAS ships a second `.ac2` file to get past it).

### 1c. The gap that no software can close: exams

Casio's exam guidance: Examination Mode blocks "add-in applications, add-in languages,
storage memory access", Python, eActivity and Program mode. Parisse states KhiCAS "is not
compatible with exam mode". So the CG50 stays exam-legal (UK, IB in exam mode, Italy's
non-CAS rule) exactly because it lacks CAS, and a CAS add-in never changes that status. The
ClassPad is on the IB "prohibited under any circumstances" list. Add-ins that fill these gaps
are **homework and learning tools, not exam tools**. Anything meant for an exam has to be
built-in behaviour, which only Casio can change.

## 2. Platform constraints for such add-ins (from the repo's memory map and the community)

| Resource | Figure | Notes |
|---|---|---|
| Official add-in RAM | 512 KB at virtual `0x08100000` (physical `0x0C160000`) | Data at the start, stack at the end; the OS heap via `malloc` is only ~128 KB. KhiCAS reports ~500 KB usable for computation after Pavel Demin's size tricks |
| Code | Runs in place from flash, 4 KB pages mapped on demand from `0x00300000` | Never copied to RAM. About 2 MB per `.g3a` |
| Extra RAM | DRAM `0x0C4E0000`–`0x0C7FFFFF` (3200 KB) is never touched by the OS, any app or the boot; verified on hardware | What KhiCAS's full build means by "a RAM section Casio does not use". gint can build a heap from any region. Undocumented, so it may differ on the fx-CG10/20 |
| On-chip RAM | IL RAM 16 KB (4 KB usable per Cemetech), X/Y RAM 8 KB each | Fast; IL RAM can hold code. Our emulator models them (RECON cont.18v) |
| CPU | SH7305, same as the ClassPad II. 118 MHz; Ptune3 overclocks | KhiCAS's author recommends Ptune3 for its 3D engine |
| SDK | gint + fxSDK (Lephenixnoir), libfxcg (Cemetech), PythonExtra 0.3.0-beta for MicroPython | gint add-ins run on our emulator (ETMU/TMU/INTC/DMAC/X/Y/IL RAM exist in Go for this) |
| Successor | fx-CG100 / Graph Math+ OS 1 and 2 have **no add-in support** | The CG50 is the last Casio with an open add-in slot, which is what makes this worthwhile |

## 3. What is worth building (ranked)

1. **Differential Equation Graph add-in (numeric, no CAS needed).** Slope/vector fields, RK4
   solution curves from tapped-in initial conditions, 2-D phase planes for autonomous systems,
   n-th order via reduction. Small (tens of KB), fits the 512 KB RAM easily, exam-irrelevant but
   the single ClassPad app with no CG50 equivalent. Our emulator is the test bench: deterministic
   screenshots of a field plot make a good regression test.
2. **A ClassPad-style front end over Giac.** KhiCAS has the engine but a terminal-like UI. A
   "Main"-like app with pretty-printed history, an Interactive menu (apply to both sides,
   substitute, factor selection) and a Verify mode would close the *feel* gap. Hard part is
   RAM: Giac itself eats most of the 500 KB, so the UI must live in the untouched 3.2 MB of DRAM
   or be a separate add-in talking to KhiCAS through files. Biggest payoff, biggest effort.
3. **Verify + Probability + Sequence as Python scripts first.** All three are small and the
   built-in Python (or PythonExtra) is enough; promote to a `.g3a` only if speed or UI demands.
4. **Two-pane layout library for gint** (graph above, algebra below, swap with a key), to
   imitate the split screen without touch. Reusable by 1 and 2.
5. Not worth building: anything needing touch, a bigger screen, or exam legality.

## 4. What this means for the emulator project

- The emulator already boots all of these: KhiCAS (two-file install exercises the `.ac2`
  mapping and the untouched DRAM), Eigenmath, gint add-ins, PythonExtra. Each is a free
  conformance workload; KhiCAS in particular stresses `malloc`, the MMU page-in path and long
  computations with AC/ON interrupts.
- A future DiffEq-Graph add-in developed against the emulator would be the first project
  add-in, and a natural subject for the planned "Writing probe add-ins" wiki page.
- Open check: does KhiCAS's extra-RAM region coincide with our measured `0x0C4E0000` boundary?
  Loading `khicas50.ac2` under the emulator with a write watch on `0x0C4E0000`–`0x0C7FFFFF`
  would confirm it (and would prove the emulator's "never written" claim holds under add-ins).

## Sources

- Casio, ClassPad II fx-CP400 features: https://www.casio-intl.com/asia/en/calc/products/ClassPadIIfx-CP400/
- Casio, ClassPad II User's Guide (built-in application list): https://casioeducation.shriro.com.au/wp-content/uploads/2020/07/ClassPadII_UG_EN.pdf
- Casio UK, fx-CG50 add-ins (3D Graph, Physium, Geometry, Picture Plot, Probability Simulation): https://education.casio.co.uk/support/os-files/os-files-cg50-add-ins/
- Casio UK, exam permissions, "does not have symbolic algebra manipulation": https://info.casio.co.uk/hubfs/CAL/Resources/Exams/Exams_all-models.pdf
- Casio Education, exam mode blocks add-ins and Python: https://www.casioeducation.com/post/calculator-exam-mode-why-and-how-with-the-fx-9750giii-and-fx-cg50-prizm
- IB, use of calculators (ClassPad prohibited): https://www.ibo.org/contentassets/40baa9d7fb72482881be9a12f217f8a9/use-of-calculators-in-examinations-2020_e.pdf
- B. Parisse, KhiCAS for Casio (features, two-file install, 500 K RAM, not exam-mode compatible): https://www-fourier.univ-grenoble-alpes.fr/~parisse/hp39/khicasioen.html
- Eigenmath for the Prizm (Cemetech): https://www.cemetech.net/downloads/files/1144
- Cemetech, "How much RAM does an add-in have access to?": https://www.cemetech.net/forum/viewtopic.php?p=292658
- Cemetech, fx-CG50 3D Graph add-in features and OS 3.10 additions: https://www.cemetech.net/forum/viewtopic.php?p=255698 and https://cemetech.net/forum/viewtopic.php?p=276554
- Cemetech, PythonExtra 0.3.0-beta on the CG50: https://www.cemetech.net/forum/viewtopic.php?p=305420
- HP Museum, Slope Field Graph Python port to the CG50: https://www.hpmuseum.org/forum/post-197690.html
- Wikipedia, Casio ClassPad 300 / ClassPad II hardware (SH7305, 320x528, 24 MB flash): https://en.wikipedia.org/wiki/Casio_ClassPad_300
- Cemetech / Omnimaga, ClassPad II teardown: https://www.cemetech.net/forum/viewtopic.php?t=9346
- ClasspadDev, PythonExtra template for the fx-CP400: https://github.com/ClasspadDev/pythonextra-template
- This repo, memory map (add-in RAM, untouched DRAM, add-in mappings): `wiki/docs/memory-map.md`
