package com.hexbinoct.cg50

import android.content.Context
import android.graphics.Bitmap
import android.graphics.Color
import android.graphics.Paint
import android.graphics.Rect
import android.util.AttributeSet
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

    /** Instructions executed per frame. ~20 M/s at 60 fps; tune for your device after measuring. */
    var instrPerFrame = 333_333

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
        emulatorReady = true
    }

    override fun surfaceCreated(holder: SurfaceHolder) = startThread()
    override fun surfaceChanged(holder: SurfaceHolder, format: Int, width: Int, height: Int) =
        dst.set(0, 0, width, height)
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
        while (running) {
            val t0 = System.nanoTime()
            if (emulatorReady) {
                NativeBridge.step(instrPerFrame)
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
