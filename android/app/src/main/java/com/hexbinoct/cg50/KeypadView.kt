package com.hexbinoct.cg50

import android.content.Context
import android.graphics.Canvas
import android.graphics.LinearGradient
import android.graphics.Paint
import android.graphics.Path
import android.graphics.RectF
import android.graphics.Shader
import android.graphics.Typeface
import android.util.AttributeSet
import android.util.Log
import android.view.HapticFeedbackConstants
import android.view.MotionEvent
import android.view.View
import kotlin.math.atan2
import kotlin.math.hypot
import kotlin.math.min

/**
 * The calculator keyboard, drawn in one view in the fx-CG50's physical arrangement (KeyMap.kt):
 * F1-F6, then SHIFT/OPTN/VARS/MENU and ALPHA/x²/^/EXIT beside a round D-pad, two six-key function
 * rows, and the four large number-pad rows. Legends sit above each key like the faceplate print.
 *
 * Touch down/up = matrix press/release (NativeBridge.keyDown/keyUp), so holding a key auto-repeats
 * with the OS's own timing. Multi-touch: each pointer holds its own key.
 */
class KeypadView @JvmOverloads constructor(context: Context, attrs: AttributeSet? = null) :
    View(context, attrs) {

    private class Slot(val key: Key) {
        val face = RectF()   // the key cap
        val hit = RectF()    // touch area (cap + its legend band)
        var legendTop = 0f
        var down = 0         // pointers holding it
    }

    private val slots = ArrayList<Slot>()
    private val dpadSlots = DPAD.map { Slot(it) } // up, down, left, right
    private var dcx = 0f
    private var dcy = 0f
    private var dr = 0f
    private val byPointer = HashMap<Int, Slot>()

    private val dp = resources.displayMetrics.density
    private val fill = Paint(Paint.ANTI_ALIAS_FLAG)
    private val stroke = Paint(Paint.ANTI_ALIAS_FLAG).apply { style = Paint.Style.STROKE }
    private val text = Paint(Paint.ANTI_ALIAS_FLAG).apply {
        textAlign = Paint.Align.CENTER
        typeface = Typeface.create("sans-serif-medium", Typeface.NORMAL)
    }
    private val legend = Paint(Paint.ANTI_ALIAS_FLAG).apply {
        typeface = Typeface.create("sans-serif-condensed", Typeface.BOLD)
    }
    private val path = Path()

    private val yellow = 0xFFF2C53D.toInt()
    private val red = 0xFFFF6B6B.toInt()

    private class Palette(val top: Int, val bottom: Int, val pressTop: Int, val pressBottom: Int, val ink: Int, val rim: Int)

    private val palettes = mapOf(
        Cap.FUNC to Palette(0xFFEEF1F5.toInt(), 0xFFC9CED6.toInt(), 0xFFC4C9D1.toInt(), 0xFFAEB4BD.toInt(), 0xFF1C2027.toInt(), 0x66FFFFFF.toInt()),
        Cap.DARK to Palette(0xFF4B515C.toInt(), 0xFF2A2E35.toInt(), 0xFF2A2E35.toInt(), 0xFF1E2126.toInt(), 0xFFF2F3F5.toInt(), 0x33FFFFFF.toInt()),
        Cap.LIGHT to Palette(0xFFFCFCFD.toInt(), 0xFFD8DCE2.toInt(), 0xFFD2D6DC.toInt(), 0xFFBCC1C8.toInt(), 0xFF14171C.toInt(), 0x88FFFFFF.toInt()),
        Cap.ACCENT to Palette(0xFF5B8FDD.toInt(), 0xFF2E5CA8.toInt(), 0xFF2E5CA8.toInt(), 0xFF244985.toInt(), 0xFFFFFFFF.toInt(), 0x44FFFFFF.toInt()),
    )

    init {
        for (k in FKEYS) slots += Slot(k)
        for (r in TOP_ROWS) for (k in r) slots += Slot(k)
        for (r in FUNC_ROWS) for (k in r) slots += Slot(k)
        for (r in NUM_ROWS) for (k in r) slots += Slot(k)
        isHapticFeedbackEnabled = true
    }

    // ---- layout -------------------------------------------------------------------------

    override fun onSizeChanged(w: Int, h: Int, oldw: Int, oldh: Int) {
        val padX = 10 * dp
        val padTop = 4 * dp
        val padBottom = 8 * dp
        val gapX = 7 * dp
        // row weights: F-row, 2 top rows, 2 function rows, 4 number rows
        val weights = floatArrayOf(0.95f, 1.05f, 1.05f, 1.05f, 1.05f, 1.3f, 1.3f, 1.3f, 1.3f)
        val unit = (h - padTop - padBottom) / weights.sum()
        val cw = w - 2 * padX
        var y = padTop
        val rowTop = FloatArray(weights.size)
        val rowH = FloatArray(weights.size)
        for (i in weights.indices) {
            rowTop[i] = y; rowH[i] = weights[i] * unit; y += rowH[i]
        }
        var s = 0
        fun layoutRow(keys: Int, left: Float, width: Float, top: Float, height: Float, capFrac: Float) {
            val kw = (width - gapX * (keys - 1)) / keys
            val legendH = height * 0.30f
            val capH = (height - legendH - height * 0.07f) * capFrac
            for (i in 0 until keys) {
                val slot = slots[s++]
                val x = left + i * (kw + gapX)
                val capTop = top + legendH + (height - legendH - height * 0.07f - capH) / 2
                slot.face.set(x, capTop, x + kw, capTop + capH)
                slot.hit.set(x - gapX / 2, top, x + kw + gapX / 2, top + height)
                slot.legendTop = top
            }
        }
        layoutRow(6, padX, cw, rowTop[0], rowH[0], 0.78f)
        val leftW = cw * 0.64f
        layoutRow(4, padX, leftW, rowTop[1], rowH[1], 1f)
        layoutRow(4, padX, leftW, rowTop[2], rowH[2], 1f)
        for (i in 0 until 2) layoutRow(6, padX, cw, rowTop[3 + i], rowH[3 + i], 1f)
        for (i in 0 until 4) layoutRow(5, padX, cw, rowTop[5 + i], rowH[5 + i], 1f)

        // D-pad: right of the two top rows, spanning both
        val regionL = padX + leftW + gapX
        val regionW = w - padX - regionL
        dcx = regionL + regionW / 2
        dcy = rowTop[1] + (rowH[1] + rowH[2]) / 2 + rowH[1] * 0.06f
        dr = min(regionW, rowH[1] + rowH[2]) / 2 * 0.94f
    }

    // ---- drawing ------------------------------------------------------------------------

    override fun onDraw(canvas: Canvas) {
        for (slot in slots) drawKey(canvas, slot)
        drawDpad(canvas)
    }

    private fun drawKey(c: Canvas, slot: Slot) {
        val k = slot.key
        val pal = palettes.getValue(k.cap)
        val f = slot.face
        val pressed = slot.down > 0
        val r = min(f.height(), f.width()) * (if (k.cap == Cap.FUNC) 0.28f else 0.22f)
        val lift = 2.5f * dp
        val sink = if (pressed) 1.5f * dp else 0f

        // legends above the cap
        val lh = f.top - slot.legendTop
        legend.textSize = min(lh * 0.62f, 11.5f * dp)
        val ly = f.top - lh * 0.28f
        if (k.cap == Cap.FUNC) {
            legend.color = yellow; legend.textAlign = Paint.Align.CENTER
            drawFit(c, legend, k.shift, f.centerX(), ly, f.width() + 4 * dp)
        } else {
            val half = f.width() * (if (k.alpha.isEmpty()) 1.1f else 0.62f)
            legend.color = yellow; legend.textAlign = Paint.Align.LEFT
            drawFit(c, legend, k.shift, f.left, ly, half)
            legend.color = red; legend.textAlign = Paint.Align.RIGHT
            drawFit(c, legend, k.alpha, f.right, ly, f.width() * 0.4f)
        }

        // shadow, then the cap with a vertical gradient and a light rim
        fill.shader = null
        fill.color = 0x99000000.toInt()
        c.drawRoundRect(f.left, f.top + lift, f.right, f.bottom + lift, r, r, fill)
        val top = f.top + sink
        val bottom = f.bottom + sink
        fill.shader = LinearGradient(0f, top, 0f, bottom,
            if (pressed) pal.pressTop else pal.top, if (pressed) pal.pressBottom else pal.bottom, Shader.TileMode.CLAMP)
        c.drawRoundRect(f.left, top, f.right, bottom, r, r, fill)
        fill.shader = null
        stroke.strokeWidth = 1f * dp
        stroke.color = pal.rim
        c.drawRoundRect(f.left + dp / 2, top + dp / 2, f.right - dp / 2, bottom - dp / 2, r, r, stroke)

        // face label
        text.color = when (k.label) {
            "SHIFT" -> yellow
            "ALPHA" -> red
            else -> pal.ink
        }
        val big = k.cap == Cap.LIGHT && k.label.length <= 2
        text.textSize = f.height() * (if (big) 0.52f else 0.36f)
        text.textAlign = Paint.Align.CENTER
        drawFit(c, text, k.label, f.centerX(), (top + bottom) / 2 - (text.descent() + text.ascent()) / 2, f.width() * 0.86f)
    }

    /** Draws s at (x,y) with the paint's alignment, shrinking the text size to fit maxW. */
    private fun drawFit(c: Canvas, p: Paint, s: String, x: Float, y: Float, maxW: Float) {
        if (s.isEmpty()) return
        val size = p.textSize
        val w = p.measureText(s)
        if (w > maxW) p.textSize = size * maxW / w
        c.drawText(s, x, y, p)
        p.textSize = size
    }

    private fun drawDpad(c: Canvas) {
        if (dr <= 0f) return
        // outer ring + body
        fill.shader = null
        fill.color = 0x99000000.toInt()
        c.drawCircle(dcx, dcy + 3 * dp, dr, fill)
        fill.shader = LinearGradient(0f, dcy - dr, 0f, dcy + dr, 0xFF565D69.toInt(), 0xFF252930.toInt(), Shader.TileMode.CLAMP)
        c.drawCircle(dcx, dcy, dr, fill)
        fill.shader = null
        stroke.strokeWidth = 1.2f * dp
        stroke.color = 0x40FFFFFF.toInt()
        c.drawCircle(dcx, dcy, dr - dp, stroke)

        // pressed wedge highlight
        val angles = floatArrayOf(-90f, 90f, 180f, 0f) // up, down, left, right (canvas degrees)
        val oval = RectF(dcx - dr, dcy - dr, dcx + dr, dcy + dr)
        for (i in 0 until 4) if (dpadSlots[i].down > 0) {
            fill.color = 0x40FFFFFF.toInt()
            c.drawArc(oval, angles[i] - 45f, 90f, true, fill)
        }
        // divider cross (diagonals) and centre boss
        stroke.color = 0x55000000.toInt()
        stroke.strokeWidth = 1.5f * dp
        val d = dr * 0.7071f
        c.drawLine(dcx - d, dcy - d, dcx + d, dcy + d, stroke)
        c.drawLine(dcx - d, dcy + d, dcx + d, dcy - d, stroke)
        fill.shader = LinearGradient(0f, dcy - dr * 0.36f, 0f, dcy + dr * 0.36f, 0xFF3A3F48.toInt(), 0xFF1D2025.toInt(), Shader.TileMode.CLAMP)
        c.drawCircle(dcx, dcy, dr * 0.36f, fill)
        fill.shader = null

        // arrows
        fill.color = 0xFFEDEFF2.toInt()
        val a = dr * 0.16f
        val m = dr * 0.68f
        fun tri(cx: Float, cy: Float, dx: Float, dy: Float) {
            path.reset()
            path.moveTo(cx + dx * a, cy + dy * a)
            path.lineTo(cx - dx * a * 0.7f - dy * a, cy - dy * a * 0.7f - dx * a)
            path.lineTo(cx - dx * a * 0.7f + dy * a, cy - dy * a * 0.7f + dx * a)
            path.close()
            c.drawPath(path, fill)
        }
        tri(dcx, dcy - m, 0f, -1f)
        tri(dcx, dcy + m, 0f, 1f)
        tri(dcx - m, dcy, -1f, 0f)
        tri(dcx + m, dcy, 1f, 0f)
    }

    // ---- touch --------------------------------------------------------------------------

    private fun slotAt(x: Float, y: Float): Slot? {
        val dist = hypot(x - dcx, y - dcy)
        if (dist <= dr * 1.05f) {
            if (dist < dr * 0.3f) return null // centre boss is not a key
            val deg = Math.toDegrees(atan2((y - dcy).toDouble(), (x - dcx).toDouble()))
            return when {
                deg >= -45 && deg < 45 -> dpadSlots[3]
                deg >= 45 && deg < 135 -> dpadSlots[1]
                deg >= -135 && deg < -45 -> dpadSlots[0]
                else -> dpadSlots[2]
            }
        }
        return slots.firstOrNull { it.hit.contains(x, y) }
    }

    override fun onTouchEvent(ev: MotionEvent): Boolean {
        when (ev.actionMasked) {
            MotionEvent.ACTION_DOWN, MotionEvent.ACTION_POINTER_DOWN -> {
                val i = ev.actionIndex
                val slot = slotAt(ev.getX(i), ev.getY(i)) ?: return true
                byPointer[ev.getPointerId(i)] = slot
                if (slot.down++ == 0) {
                    val t = System.nanoTime()
                    NativeBridge.keyDown(slot.key.row, slot.key.col)
                    Log.i("cg50-key", "ui down ${slot.key.label} jni=${(System.nanoTime() - t) / 1000}us")
                }
                performHapticFeedback(HapticFeedbackConstants.KEYBOARD_TAP)
                invalidate()
            }
            MotionEvent.ACTION_UP, MotionEvent.ACTION_POINTER_UP -> {
                release(ev.getPointerId(ev.actionIndex))
                if (ev.actionMasked == MotionEvent.ACTION_UP) performClick()
            }
            MotionEvent.ACTION_CANCEL -> byPointer.keys.toList().forEach { release(it) }
        }
        return true
    }

    private fun release(pointer: Int) {
        val slot = byPointer.remove(pointer) ?: return
        if (--slot.down == 0) {
            val t = System.nanoTime()
            NativeBridge.keyUp(slot.key.row, slot.key.col)
            Log.i("cg50-key", "ui up   ${slot.key.label} jni=${(System.nanoTime() - t) / 1000}us")
        }
        invalidate()
    }

    override fun performClick(): Boolean {
        super.performClick()
        return true
    }
}
