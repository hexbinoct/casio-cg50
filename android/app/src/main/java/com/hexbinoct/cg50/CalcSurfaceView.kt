package com.hexbinoct.cg50

import android.annotation.SuppressLint
import android.content.Context
import android.graphics.Bitmap
import android.graphics.Color
import android.graphics.Paint
import android.graphics.Rect
import android.os.Build
import android.os.PerformanceHintManager
import android.os.Process
import android.util.AttributeSet
import android.util.Log
import android.view.SurfaceHolder
import android.view.SurfaceView
import java.nio.ByteBuffer

/**
 * Draws the emulator's framebuffer and drives the machine, on two threads:
 *
 *  - `cg50-emu` runs the core in CHUNK-instruction slices paced against real time: it steps
 *    whenever emulated time is behind the wall clock and sleeps only when it is ahead. An idle
 *    machine (sleeping CPU, fast-forwarded in the core) costs nearly nothing; a busy one runs
 *    this thread flat out. That matters on big.LITTLE phones: the scheduler places a thread by
 *    its *running* utilisation, and the old single loop (step ~10 ms, then block in
 *    lockCanvas, then sleep) looked like a 40 % task that "fits" a little core, so key-repeat
 *    bursts ran on an A55 at 1 GHz with the big cores idle. A saturated thread is migrated up
 *    within a few tens of ms.
 *  - `cg50-render` pulls the RGBA frame and blits it (scaled by the compositor) at ~60 fps.
 *
 * The emulator must be init'd/resumed (MainActivity does that) before onEmulatorReady().
 */
class CalcSurfaceView @JvmOverloads constructor(context: Context, attrs: AttributeSet? = null) :
    SurfaceView(context, attrs), SurfaceHolder.Callback {

    @Volatile private var running = false
    @Volatile private var emulatorReady = false
    private var emuThread: Thread? = null
    private var renderThread: Thread? = null

    private var w = 0
    private var h = 0
    private lateinit var buf: ByteArray
    private lateinit var bmp: Bitmap
    private val src = Rect()
    private val dst = Rect()
    private val paint = Paint().apply { isFilterBitmap = false } // crisp pixels

    /** `am start ... --ez adpf false` runs without the ADPF hint session (A/B measuring). */
    @Volatile var useHints = true

    /**
     * Time base: an emulated second is always `ips` instructions, so the OS's clocks, key
     * repeat and CPU work stay consistent; the host does not feed a measured rate back in.
     * IPS_ORIGINAL reproduces the real fx-CG50: measured against a held key on the calculator
     * (11-12 menu moves in 1.2 s), its effective throughput on OS code is ~45M instr/s — NOR
     * flash wait states and SDRAM VRAM writes cost the 118 MHz SH7305 more than half its clock.
     * IPS_FAST is the bare clock, about twice as fast as the real thing. The speed setting
     * (MainActivity, long-press the screen) switches between them at run time.
     *
     * Pacing: the emu thread advances ips/200 instructions (= CHUNK_NS of emulated time) each
     * time the wall clock reaches the chunk's due time. If the phone can't keep up, the machine
     * falls behind real time; once the debt exceeds MAX_DEBT_NS it is dropped, so the whole
     * machine slows uniformly — like a slower calculator — rather than its timers drifting
     * against its CPU work. (A per-second setInstrPerSec feedback did exactly that: idle seconds
     * raised the rate, then key repeat ran at half speed while busy.)
     */
    companion object {
        const val IPS_ORIGINAL = 45_000_000
        const val IPS_FAST = 100_000_000
        const val CHUNK_NS = 1_000_000_000L / 200       // 5 ms of emulated time per chunk
        const val MAX_DEBT_NS = 20_000_000L
        const val FRAME_NS = 1_000_000_000L / 60
        const val WARM_NS = 300_000_000L
    }

    /** Instructions per emulated second; MainActivity keeps it equal to the core's setInstrPerSec. */
    @Volatile var ips = IPS_ORIGINAL

    init {
        holder.addCallback(this)
    }

    /** Height follows width at the panel's 384:216 aspect, so pixels stay square. */
    override fun onMeasure(widthMeasureSpec: Int, heightMeasureSpec: Int) {
        val width = MeasureSpec.getSize(widthMeasureSpec)
        setMeasuredDimension(width, width * 216 / 384)
    }

    /** Call after NativeBridge.init()/resume() so the loop can allocate the frame buffers. */
    fun onEmulatorReady() {
        w = NativeBridge.width()
        h = NativeBridge.height()
        buf = ByteArray(w * h * 4)
        bmp = Bitmap.createBitmap(w, h, Bitmap.Config.ARGB_8888)
        src.set(0, 0, w, h)
        dst.set(0, 0, w, h)
        // Render at native resolution and let the display compositor (GPU) scale the surface up to
        // the view bounds — far cheaper than scaling 384x216 -> full screen in software each frame.
        holder.setFixedSize(w, h)
        emulatorReady = true
    }

    override fun surfaceCreated(holder: SurfaceHolder) = startThreads()
    override fun surfaceChanged(holder: SurfaceHolder, format: Int, width: Int, height: Int) {
        // dst tracks the canvas buffer, which is the fixed native size once setFixedSize takes effect.
        dst.set(0, 0, width, height)
        needsRedraw = true
    }

    /** Set when the surface itself changed, so the next frame is drawn even if the panel didn't. */
    @Volatile private var needsRedraw = true
    override fun surfaceDestroyed(holder: SurfaceHolder) = stopThreads()

    fun pauseRendering() = stopThreads()
    fun resumeRendering() {
        if (emuThread == null && holder.surface?.isValid == true) startThreads()
    }

    private fun startThreads() {
        if (emuThread != null) return
        running = true
        emuThread = Thread(::emuLoop, "cg50-emu").also { it.start() }
        renderThread = Thread(::renderLoop, "cg50-render").also { it.start() }
    }

    private fun stopThreads() {
        running = false
        emuThread?.join(1000)
        renderThread?.join(1000)
        emuThread = null
        renderThread = null
    }

    private fun sleepNs(ns: Long) {
        try {
            Thread.sleep(ns / 1_000_000, (ns % 1_000_000).toInt())
        } catch (_: InterruptedException) {
        }
    }

    /**
     * ADPF: a performance-hint session for the emu thread (API 31+). Each chunk reports its
     * actual duration against the CHUNK_NS target so the kernel raises the clocks as soon as the
     * emulated OS gets busy. Null on older devices or where the power HAL lacks hint sessions
     * (e.g. the POCO X3 / Android 12) — then the thread's own saturation has to do it.
     */
    private fun createHintSession(targetNs: Long): PerformanceHintManager.Session? {
        if (!useHints || Build.VERSION.SDK_INT < Build.VERSION_CODES.S) return null
        return try {
            context.getSystemService(PerformanceHintManager::class.java)
                ?.createHintSession(intArrayOf(Process.myTid()), targetNs)
                .also { Log.i("cg50-perf", "ADPF hint session: ${if (it != null) "on" else "unavailable"}") }
        } catch (e: Exception) {
            Log.w("cg50-perf", "ADPF hint session failed: $e")
            null
        }
    }

    @SuppressLint("NewApi") // `hint` is only non-null on API 31+ (createHintSession)
    private fun emuLoop() {
        Process.setThreadPriority(Process.THREAD_PRIORITY_DISPLAY)
        val hint = createHintSession(CHUNK_NS)
        var due = System.nanoTime()   // wall time at which the next chunk is due
        // --- perf instrumentation (logcat tag "cg50-perf"): accumulate over ~1s windows ---
        var statWindowStartNs = System.nanoTime()
        var statChunks = 0L         // chunks run this window (emulated time = chunks * CHUNK_NS)
        var statInstr = 0L          // emulated instructions this window
        var statStepNs = 0L         // wall-time spent inside step()
        var statDrops = 0           // times the debt was dropped (phone couldn't keep up)
        var statExecuted0 = NativeBridge.executed()
        while (running) {
            if (!emulatorReady) {
                sleepNs(CHUNK_NS)
                due = System.nanoTime()
                continue
            }
            val now = System.nanoTime()
            if (due > now) {
                sleepNs(due - now)
                continue
            }
            val chunk = ips / 200
            val c0 = NativeBridge.cycles()
            NativeBridge.step(chunk)
            val t1 = System.nanoTime()
            val stepNs = t1 - now
            hint?.reportActualWorkDuration(stepNs.coerceIn(1L, 200_000_000L))
            // Emulated time that actually passed: a step overshoots when a native blit charges
            // its cycles at once (or a key injection runs extra instructions); pacing on the
            // requested chunk would let the machine run fast. A halted core (0 cycles) still
            // advances so this loop can't spin.
            val ran = NativeBridge.cycles() - c0
            due += if (ran > 0) ran * 1_000_000_000L / ips else CHUNK_NS
            if (t1 - due > MAX_DEBT_NS) {
                due = t1
                statDrops++
            }
            statChunks++
            statInstr += ran
            statStepNs += stepNs
            // Report once per ~1s: emulated vs executed instr/s, how busy the thread was, drops.
            val winNs = t1 - statWindowStartNs
            if (winNs >= 1_000_000_000L) {
                val executed = NativeBridge.executed()
                Log.i(
                    "cg50-perf",
                    "emu: emulated=%.1fM/s (time base=%dM) executed=%.2fM/s busy=%.0f%% step=%.2fms/chunk drops=%d".format(
                        statInstr * 1e3 / winNs, ips / 1_000_000,
                        (executed - statExecuted0) * 1e3 / winNs,
                        statStepNs * 100.0 / winNs, statStepNs / 1e6 / statChunks, statDrops
                    )
                )
                statWindowStartNs = t1
                statChunks = 0L; statInstr = 0L; statStepNs = 0L; statDrops = 0; statExecuted0 = executed
            }
        }
        hint?.close()
    }

    private fun renderLoop() {
        var statWindowStartNs = System.nanoTime()
        var statBlitNs = 0L         // wall-time spent pulling+blitting the frame
        var statFrames = 0          // render-loop iterations this window
        var statDrawn = 0           // frames actually blitted (panel changed or surface did)
        var statPushes0 = NativeBridge.pushes() // LCD frame pushes at window start (= OS redraws)
        var drawnGen = -1L          // panel generation of the last blitted frame
        var changedNs = 0L          // when the panel last changed
        while (running) {
            val t0 = System.nanoTime()
            if (emulatorReady) {
                val gen = NativeBridge.frameGen()
                if (gen != drawnGen) changedNs = t0
                // Keep drawing every frame for a while after a change: a surface that is only
                // drawn now and then takes 20-30 ms per draw (cold buffer path) instead of ~9,
                // which is latency the user sees during typing. Truly idle = no draws at all.
                if (t0 - changedNs < WARM_NS || needsRedraw) {
                    needsRedraw = false
                    drawnGen = gen
                    NativeBridge.framebufferRGBA(buf)
                    bmp.copyPixelsFromBuffer(ByteBuffer.wrap(buf))
                    val c = holder.lockCanvas()
                    if (c != null) {
                        try {
                            c.drawColor(Color.BLACK)
                            if (!dst.isEmpty) c.drawBitmap(bmp, src, dst, paint)
                        } finally {
                            holder.unlockCanvasAndPost(c)
                        }
                    }
                    statBlitNs += System.nanoTime() - t0
                    statDrawn++
                }
                statFrames++
            }
            val winNs = System.nanoTime() - statWindowStartNs
            if (winNs >= 1_000_000_000L && statFrames > 0) {
                val pushes = NativeBridge.pushes()
                Log.i(
                    "cg50-perf",
                    "render: fps=%.1f drawn=%d blit=%.1fms/drawn pushes=%d".format(
                        statFrames * 1_000_000_000.0 / winNs, statDrawn,
                        statBlitNs / 1e6 / maxOf(statDrawn, 1), pushes - statPushes0
                    )
                )
                statWindowStartNs = System.nanoTime()
                statBlitNs = 0L; statFrames = 0; statDrawn = 0; statPushes0 = pushes
            }
            val sleep = FRAME_NS - (System.nanoTime() - t0)
            if (sleep > 0) sleepNs(sleep)
        }
    }
}
