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

    init {
        holder.addCallback(this)
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
        while (running) {
            val t0 = System.nanoTime()
            if (emulatorReady) {
                val tStep = System.nanoTime()
                NativeBridge.step(instrPerFrame)
                val tBlit = System.nanoTime()
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
                    "ips=%.2fM fps=%.1f step=%.1fms/f blit=%.1fms/f (budget=%d)".format(
                        ips / 1e6, fps,
                        statStepNs / 1e6 / statFrames, statBlitNs / 1e6 / statFrames,
                        instrPerFrame
                    )
                )
                statWindowStartNs = System.nanoTime()
                statInstr = 0L; statStepNs = 0L; statBlitNs = 0L; statFrames = 0
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
