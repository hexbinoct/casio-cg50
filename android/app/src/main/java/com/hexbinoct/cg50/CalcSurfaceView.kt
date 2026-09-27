package com.hexbinoct.cg50

import android.content.Context
import android.graphics.Bitmap
import android.graphics.Color
import android.graphics.Paint
import android.graphics.Rect
import android.util.AttributeSet
import android.util.Log
import android.view.SurfaceHolder
import android.view.SurfaceView
import java.nio.ByteBuffer

/**
 * Draws the emulator's framebuffer and drives the run loop on its own thread: each frame it
 * steps the core a fixed instruction budget, pulls the RGBA frame, and blits it (scaled,
 * nearest-neighbour) to the surface. Paced to ~60 fps. The emulator must be init'd/resumed
 * (MainActivity does that) before onEmulatorReady() is called.
 */
class CalcSurfaceView @JvmOverloads constructor(context: Context, attrs: AttributeSet? = null) :
    SurfaceView(context, attrs), SurfaceHolder.Callback, Runnable {

    @Volatile private var running = false
    @Volatile private var emulatorReady = false
    private var thread: Thread? = null

    private var w = 0
    private var h = 0
    private lateinit var buf: ByteArray
    private lateinit var bmp: Bitmap
    private val src = Rect()
    private val dst = Rect()
    private val paint = Paint().apply { isFilterBitmap = false } // crisp pixels

    /**
     * Instructions executed per frame. Measured raw core speed on arm64 is ~29 M/s, so with a
     * cheap (GPU-composited) blit we budget ~30 M/s at 60 fps. Tune per device after measuring.
     */
    var instrPerFrame = 500_000

    /**
     * Time base: the emulated machine runs at MAX_IPS — about the real fx-CG50 (SH7305 at
     * ~118 MHz) — and that never changes (MainActivity sets it once). An emulated second is
     * always MAX_IPS instructions, so the OS's clocks, key repeat and CPU work stay consistent.
     *
     * Budget: each frame runs up to MAX_IPS/60 instructions (real time), sized so step() takes
     * about STEP_TARGET_NS of the 16.7 ms frame (the rest is blit + UI), changing by at most
     * ±25% per frame. Idle frames are cheap (a sleeping CPU fast-forwards in the core), so the
     * budget sits at real time; when this phone can't keep up with heavy OS work the budget
     * shrinks and the whole machine slows uniformly — like a slower calculator — instead of the
     * OS's timers drifting against its CPU work. (A per-second setInstrPerSec feedback did
     * exactly that: idle seconds raised the rate, then key repeat ran at half speed while busy.)
     */
    companion object {
        const val MAX_IPS = 100_000_000
        const val MIN_PER_FRAME = 200_000
        const val STEP_TARGET_NS = 11_000_000L
    }

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

    override fun surfaceCreated(holder: SurfaceHolder) = startThread()
    override fun surfaceChanged(holder: SurfaceHolder, format: Int, width: Int, height: Int) {
        // dst tracks the canvas buffer, which is the fixed native size once setFixedSize takes effect.
        dst.set(0, 0, width, height)
    }
    override fun surfaceDestroyed(holder: SurfaceHolder) = stopThread()

    fun pauseRendering() = stopThread()
    fun resumeRendering() {
        if (thread == null && holder.surface?.isValid == true) startThread()
    }

    private fun startThread() {
        if (thread != null) return
        running = true
        thread = Thread(this, "cg50-render").also { it.start() }
    }

    private fun stopThread() {
        running = false
        thread?.join(500)
        thread = null
    }

    override fun run() {
        val frameNs = 1_000_000_000L / 60
        // --- perf instrumentation (logcat tag "cg50-perf"): accumulate over ~1s windows ---
        var statWindowStartNs = System.nanoTime()
        var statInstr = 0L          // emulated instructions executed this window
        var statStepNs = 0L         // wall-time spent inside step()
        var statBlitNs = 0L         // wall-time spent pulling+blitting the frame
        var statFrames = 0          // render-loop iterations this window
        var statExecuted = 0L       // instructions actually executed (idle excluded)
        while (running) {
            val t0 = System.nanoTime()
            if (emulatorReady) {
                val exec0 = NativeBridge.executed()
                val tStep = System.nanoTime()
                NativeBridge.step(instrPerFrame)
                val tBlit = System.nanoTime()
                val executed = NativeBridge.executed() - exec0
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
                val tEnd = System.nanoTime()
                statInstr += instrPerFrame
                statExecuted += executed
                val stepNs = maxOf(tBlit - tStep, 1L)
                instrPerFrame = (instrPerFrame * STEP_TARGET_NS / stepNs)
                    .coerceIn(instrPerFrame * 3L / 4, instrPerFrame * 5L / 4)
                    .coerceIn(MIN_PER_FRAME.toLong(), (MAX_IPS / 60).toLong())
                    .toInt()
                statStepNs += tBlit - tStep
                statBlitNs += tEnd - tBlit
                statFrames++
            }
            // Report once per ~1s: achieved emulated instr/s, render fps, and where time went.
            val winNs = System.nanoTime() - statWindowStartNs
            if (winNs >= 1_000_000_000L && statFrames > 0) {
                val ips = statInstr * 1_000_000_000.0 / winNs
                val fps = statFrames * 1_000_000_000.0 / winNs
                Log.i(
                    "cg50-perf",
                    "emulated=%.2fM/s (real calc=%dM) executed=%.2fM/s fps=%.1f step=%.1fms/f blit=%.1fms/f budget=%d".format(
                        ips / 1e6, MAX_IPS / 1_000_000, statExecuted * 1e3 / winNs,
                        fps, statStepNs / 1e6 / statFrames, statBlitNs / 1e6 / statFrames,
                        instrPerFrame
                    )
                )
                statWindowStartNs = System.nanoTime()
                statInstr = 0L; statStepNs = 0L; statBlitNs = 0L; statFrames = 0; statExecuted = 0L
            }
            val sleep = frameNs - (System.nanoTime() - t0)
            if (sleep > 0) {
                try {
                    Thread.sleep(sleep / 1_000_000, (sleep % 1_000_000).toInt())
                } catch (_: InterruptedException) {
                }
            }
        }
    }
}
