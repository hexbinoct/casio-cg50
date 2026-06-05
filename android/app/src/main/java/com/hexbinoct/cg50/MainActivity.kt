package com.hexbinoct.cg50

import android.os.Bundle
import android.util.Log
import android.view.ViewGroup
import android.widget.Button
import android.widget.LinearLayout
import androidx.appcompat.app.AppCompatActivity
import com.hexbinoct.cg50.databinding.ActivityMainBinding
import java.io.File

/**
 * Drives the emulator: loads the user's flash dump + (optional) save-state from the app's
 * external files dir, builds the on-screen keypad, runs the screen via CalcSurfaceView, and
 * snapshots on pause so the next launch resumes exactly where it left off.
 *
 * Put your own files here (the app ships NO Casio firmware):
 *   adb push flash_full.bin  /sdcard/Android/data/com.hexbinoct.cg50/files/
 *   adb push cg50_state.bin  /sdcard/Android/data/com.hexbinoct.cg50/files/   (optional, recommended)
 * cg50_state.bin is a save-state provisioned to the MAIN MENU (see android/README + the
 * desktop `provision` mode); without it the app cold-boots into first-boot setup.
 */
class MainActivity : AppCompatActivity() {

    private lateinit var binding: ActivityMainBinding
    private val stateFile get() = File(getExternalFilesDir(null), "cg50_state.bin")

    companion object { private const val TAG = "cg50" }

    override fun onCreate(savedInstanceState: Bundle?) {
        super.onCreate(savedInstanceState)
        binding = ActivityMainBinding.inflate(layoutInflater)
        setContentView(binding.root)

        buildKeypad()

        val dir = getExternalFilesDir(null)
        val flash = File(dir, "flash_full.bin")
        if (!flash.exists()) {
            binding.statusText.text =
                "Missing flash_full.bin.\nadb push your dump to:\n${dir?.absolutePath}"
            return
        }

        NativeBridge.init(flash.readBytes())
        Log.i(TAG, "init ok; core ${NativeBridge.width()}x${NativeBridge.height()}")
        binding.statusText.text = if (stateFile.exists()) {
            val r = NativeBridge.resume(stateFile.readBytes())
            Log.i(TAG, "resume returned $r")
            if (r == 0) "resumed" else "resume failed ($r) — booting"
        } else {
            Log.i(TAG, "no save-state; cold boot")
            "no save-state — cold boot (first-boot setup will show)"
        }
        binding.screenView.onEmulatorReady()
    }

    private fun buildKeypad() {
        for (row in KEYPAD) {
            val rowLayout = LinearLayout(this).apply {
                orientation = LinearLayout.HORIZONTAL
                layoutParams = LinearLayout.LayoutParams(
                    ViewGroup.LayoutParams.MATCH_PARENT, 0, 1f
                )
            }
            for (key in row) {
                val b = Button(this).apply {
                    text = key.label
                    isAllCaps = false
                    layoutParams = LinearLayout.LayoutParams(0, ViewGroup.LayoutParams.MATCH_PARENT, 1f)
                    setOnClickListener { NativeBridge.injectKey(key.row, key.col) }
                }
                rowLayout.addView(b)
            }
            binding.keypad.addView(rowLayout)
        }
    }

    override fun onPause() {
        super.onPause()
        binding.screenView.pauseRendering()
        // Persist the session so the next launch resumes here (the OS's backup-battery RAM
        // is captured in the save-state; flash-only persistence isn't enough).
        try {
            NativeBridge.snapshot()?.let { stateFile.writeBytes(it) }
        } catch (_: Exception) {
        }
    }

    override fun onResume() {
        super.onResume()
        binding.screenView.resumeRendering()
    }
}
