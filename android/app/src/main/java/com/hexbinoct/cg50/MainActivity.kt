package com.hexbinoct.cg50

import android.content.Intent
import android.net.Uri
import android.os.Build
import android.os.Bundle
import android.provider.OpenableColumns
import android.util.Log
import android.widget.Toast
import androidx.activity.result.contract.ActivityResultContracts
import androidx.appcompat.app.AlertDialog
import androidx.appcompat.app.AppCompatActivity
import com.hexbinoct.cg50.databinding.ActivityMainBinding
import java.io.File

/**
 * Drives the emulator: loads the user's flash dump + (optional) save-state from the app's
 * external files dir, runs the screen via CalcSurfaceView (the keypad is KeypadView), and
 * snapshots on pause so the next launch resumes exactly where it left off.
 *
 * Put your own files here (the app ships NO Casio firmware):
 *   adb push flash_full_32mb.bin  /sdcard/Android/data/com.hexbinoct.cg50/files/
 *   adb push cg50_state_32mb.bin  /sdcard/Android/data/com.hexbinoct.cg50/files/   (recommended)
 * The whole 32 MB flash holds the calculator's storage memory, so installed add-ins and their
 * files work; the older 16 MB pair (flash_full.bin + cg50_state.bin) is used when the 32 MB
 * dump is absent. The state is a save-state at the MAIN MENU (see android/README); without it
 * the app cold-boots.
 *
 * Add-ins (.g3a) are installed into the emulated storage memory by the OS itself
 * (NativeBridge.installAddin): from the settings dialog (long-press the screen), by opening or
 * sharing a .g3a file with the app, or over adb:
 *   adb push X.g3a /sdcard/Android/data/com.hexbinoct.cg50/files/
 *   adb shell am start -n com.hexbinoct.cg50/.MainActivity --es install X.g3a
 */
class MainActivity : AppCompatActivity() {

    private lateinit var binding: ActivityMainBinding
    private lateinit var stateFile: File
    private var running = false

    companion object {
        private const val TAG = "cg50"
        private const val PREF_SPEED = "speed"
    }

    private val pickAddin = registerForActivityResult(ActivityResultContracts.OpenDocument()) { uri ->
        uri?.let { installFrom(it) }
    }

    override fun onCreate(savedInstanceState: Bundle?) {
        super.onCreate(savedInstanceState)
        binding = ActivityMainBinding.inflate(layoutInflater)
        setContentView(binding.root)

        val dir = getExternalFilesDir(null)
        val big = File(dir, "flash_full_32mb.bin")
        val flash = if (big.exists()) big else File(dir, "flash_full.bin")
        stateFile = File(dir, if (big.exists()) "cg50_state_32mb.bin" else "cg50_state.bin")
        if (!flash.exists()) {
            binding.statusText.text =
                "Missing flash_full_32mb.bin (or flash_full.bin).\nadb push your dump to:\n${dir?.absolutePath}"
            return
        }

        NativeBridge.init(flash.readBytes())
        running = true
        Log.i(TAG, "init ok (${flash.name}); core ${NativeBridge.width()}x${NativeBridge.height()}")
        binding.statusText.text = if (stateFile.exists()) {
            val r = NativeBridge.resume(stateFile.readBytes())
            Log.i(TAG, "resume ${stateFile.name} returned $r")
            if (r == 0) "resumed" else "resume failed ($r) — booting"
        } else {
            Log.i(TAG, "no save-state; cold boot")
            "no save-state — cold boot (first-boot setup will show)"
        }
        // A/B switch for measuring: `am start ... --ez adpf false` runs without the ADPF hint session.
        binding.screenView.useHints = intent.getBooleanExtra("adpf", true)
        binding.screenView.onEmulatorReady()
        // The time base: an emulated second = this many instructions; the core derives the RTC
        // periodic interrupt (cursor blink / idle heartbeat), the 32.768 kHz counter and the 33 Hz
        // key scan from it. Constant while running — see CalcSurfaceView.ips — but the speed
        // setting switches it (`am start ... --es speed fast|original` overrides for measuring).
        intent.getStringExtra("speed")?.let { prefs.edit().putString(PREF_SPEED, it).apply() }
        applySpeed()
        NativeBridge.setClock(System.currentTimeMillis() / 1000L)
        // Settings take no screen space: long-press the calculator screen.
        binding.screenView.setOnLongClickListener { showSettings(); true }
        binding.statusText.text = getString(R.string.hint_settings)
        if (savedInstanceState == null) handleInstallIntent(intent) // not again on rotation
    }

    override fun onNewIntent(intent: Intent) {
        super.onNewIntent(intent)
        setIntent(intent)
        handleInstallIntent(intent)
    }

    private val prefs get() = getSharedPreferences("cg50", MODE_PRIVATE)
    private val fast get() = prefs.getString(PREF_SPEED, "original") == "fast"

    private fun applySpeed() {
        val ips = if (fast) CalcSurfaceView.IPS_FAST else CalcSurfaceView.IPS_ORIGINAL
        NativeBridge.setInstrPerSec(ips.toLong())
        binding.screenView.ips = ips
        Log.i(TAG, "speed: ${if (fast) "fast" else "original"} ($ips instr/s)")
    }

    /** Speed: original hardware (authentic) vs fast (the SH7305's full clock, about 2x); install. */
    private fun showSettings() {
        val labels = arrayOf(getString(R.string.speed_original), getString(R.string.speed_fast))
        AlertDialog.Builder(this)
            .setTitle(R.string.settings_speed)
            .setSingleChoiceItems(labels, if (fast) 1 else 0) { dialog, which ->
                prefs.edit().putString(PREF_SPEED, if (which == 1) "fast" else "original").apply()
                applySpeed()
                dialog.dismiss()
            }
            .setNeutralButton(R.string.install_addin) { _, _ -> pickAddin.launch(arrayOf("*/*")) }
            .setNegativeButton(android.R.string.cancel, null)
            .show()
    }

    /** A .g3a opened with / shared to the app, or `--es install NAME` (a file in our files dir). */
    private fun handleInstallIntent(intent: Intent) {
        intent.getStringExtra("install")?.let { name ->
            val f = File(getExternalFilesDir(null), name)
            if (f.exists()) install(f.name, f.readBytes()) else toast(getString(R.string.install_missing, f.path))
            return
        }
        val uri: Uri? = when (intent.action) {
            Intent.ACTION_VIEW -> intent.data
            Intent.ACTION_SEND -> if (Build.VERSION.SDK_INT >= 33) {
                intent.getParcelableExtra(Intent.EXTRA_STREAM, Uri::class.java)
            } else {
                @Suppress("DEPRECATION") intent.getParcelableExtra(Intent.EXTRA_STREAM)
            }
            else -> null
        }
        uri?.let { installFrom(it) }
    }

    private fun installFrom(uri: Uri) {
        var name = displayName(uri) ?: uri.lastPathSegment?.substringAfterLast('/') ?: "addin.g3a"
        if (!name.endsWith(".g3a", ignoreCase = true)) name += ".g3a" // the core checks the header
        val data = try {
            contentResolver.openInputStream(uri)?.use { it.readBytes() }
        } catch (e: Exception) {
            Log.w(TAG, "read $uri: $e")
            null
        }
        if (data == null) toast(getString(R.string.install_unreadable, name)) else install(name, data)
    }

    private fun displayName(uri: Uri): String? = try {
        contentResolver.query(uri, arrayOf(OpenableColumns.DISPLAY_NAME), null, null, null)?.use { c ->
            if (c.moveToFirst()) c.getString(0) else null
        }
    } catch (_: Exception) {
        null
    }

    /**
     * The OS writes the file into the storage memory and the MAIN MENU is rebuilt with the new
     * icon (a few seconds of emulated work, on a worker thread; the screen just pauses). The
     * session is saved right after, so the add-in survives the app being killed.
     */
    private fun install(name: String, data: ByteArray) {
        if (!running) {
            toast(getString(R.string.install_no_emulator))
            return
        }
        if (NativeBridge.busy) {
            toast(getString(R.string.install_busy))
            return
        }
        toast(getString(R.string.install_started, name))
        NativeBridge.busy = true
        Thread {
            val err = try {
                NativeBridge.installAddin(name, data)
            } finally {
                NativeBridge.busy = false
            }
            NativeBridge.releaseAllKeys() // keys touched meanwhile were not passed on
            if (err == null) saveState()
            Log.i(TAG, "install $name (${data.size} bytes): ${err ?: "ok"}")
            runOnUiThread {
                toast(if (err == null) getString(R.string.install_done, name) else getString(R.string.install_failed, err))
            }
        }.start()
    }

    private fun toast(msg: String) = Toast.makeText(this, msg, Toast.LENGTH_LONG).show()

    private fun saveState() {
        try {
            NativeBridge.snapshot()?.let { stateFile.writeBytes(it) }
        } catch (e: Exception) {
            Log.w(TAG, "snapshot: $e")
        }
    }

    override fun onPause() {
        super.onPause()
        binding.screenView.pauseRendering()
        if (!running) return
        NativeBridge.releaseAllKeys()
        // Persist the session so the next launch resumes here (the OS's backup-battery RAM
        // is captured in the save-state; flash-only persistence isn't enough).
        saveState()
    }

    override fun onResume() {
        super.onResume()
        binding.screenView.resumeRendering()
    }
}
