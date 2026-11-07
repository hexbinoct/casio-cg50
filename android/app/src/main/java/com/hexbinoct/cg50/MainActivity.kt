package com.hexbinoct.cg50

import android.content.Intent
import android.net.Uri
import android.os.Build
import android.os.Bundle
import android.util.Log
import android.view.View
import android.widget.Toast
import androidx.activity.result.contract.ActivityResultContracts
import androidx.appcompat.app.AlertDialog
import androidx.appcompat.app.AppCompatActivity
import com.hexbinoct.cg50.databinding.ActivityMainBinding
import com.hexbinoct.cg50.databinding.DialogAppIconBinding
import com.hexbinoct.cg50.databinding.DialogSettingsBinding
import com.hexbinoct.cg50.databinding.ItemAppIconBinding
import java.io.File

/**
 * Drives the emulator: loads the user's flash dump + its save-state (DumpStore), runs the screen
 * via CalcSurfaceView (the keypad is KeypadView), and snapshots on pause so the next launch
 * resumes exactly where it left off.
 *
 * The app ships NO Casio firmware. On the first start (no dump yet) it shows a screen that asks
 * for the user's own dump and opens the system file picker; the file is checked (16 or 32 MB,
 * the CASIO boot header, the same rule as the desktop app) and copied into the external files
 * dir. "Change flash dump…" in the settings dialog does the same later. Each dump has its own
 * session (sessions/<hash>.state), so states never get mixed between dumps.
 *
 * Pushing files with adb still works (used when no dump was chosen in the app):
 *   adb push flash_full_32mb.bin  /sdcard/Android/data/com.hexbinoct.cg50/files/
 *   adb push cg50_state_32mb.bin  /sdcard/Android/data/com.hexbinoct.cg50/files/   (optional)
 * The whole 32 MB flash holds the calculator's storage memory, so installed add-ins and their
 * files work; the older 16 MB pair (flash_full.bin + cg50_state.bin) is used when the 32 MB
 * dump is absent. A pushed state (a save-state at the MAIN MENU, see android/README) is taken
 * over as that dump's session on the next start; without one the app cold-boots.
 *
 * Add-ins (.g3a) are installed into the emulated storage memory by the OS itself
 * (NativeBridge.installAddin): from the settings dialog (long-press the screen), by opening or
 * sharing a .g3a file with the app, or over adb:
 *   adb push X.g3a /sdcard/Android/data/com.hexbinoct.cg50/files/
 *   adb shell am start -n com.hexbinoct.cg50/.MainActivity --es install X.g3a
 */
class MainActivity : AppCompatActivity() {

    private lateinit var binding: ActivityMainBinding
    private lateinit var store: DumpStore
    private lateinit var stateFile: File
    private var dump: DumpStore.Dump? = null
    private var running = false
    /** A dump is being checked and copied (a worker thread); ignore further picks meanwhile. */
    private var importing = false

    companion object {
        private const val TAG = "cg50"
        private const val PREF_SPEED = "speed"
    }

    private val pickAddin = registerForActivityResult(ActivityResultContracts.OpenDocument()) { uri ->
        uri?.let { installFrom(it) }
    }

    private val pickDump = registerForActivityResult(ActivityResultContracts.OpenDocument()) { uri ->
        uri?.let { importDump(it) }
    }

    override fun onCreate(savedInstanceState: Bundle?) {
        super.onCreate(savedInstanceState)
        binding = ActivityMainBinding.inflate(layoutInflater)
        setContentView(binding.root)
        store = DumpStore(this)
        binding.chooseDump.setOnClickListener { launchDumpPicker() }

        val file = store.activeFile()
        if (file == null) {
            Log.i(TAG, "no flash dump yet: first-start screen")
            showWelcome(null)
            return
        }
        val loaded = store.load(file)
        val d = loaded.dump
        if (d == null) {
            Log.w(TAG, "${file.path}: ${loaded.problem}")
            showWelcome(problemText(loaded.problem!!))
            return
        }
        dump = d
        stateFile = store.stateFile(d)

        NativeBridge.init(loaded.bytes!!) // the core keeps its own copy
        running = true
        Log.i(TAG, "init ok (${file.name}, ${d.size shr 20} MB, id ${d.hash}); core ${NativeBridge.width()}x${NativeBridge.height()}")
        binding.statusText.text = if (stateFile.exists()) {
            val r = NativeBridge.resume(stateFile.readBytes())
            Log.i(TAG, "resume ${stateFile.name} returned $r")
            if (r == 0) "resumed" else "resume failed ($r) — booting"
        } else {
            Log.i(TAG, "no save-state (${stateFile.name}); cold boot")
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
        warnAboutOS(d)
    }

    /** The dump's size and OS version, for the settings and the change-dump dialog. */
    private fun dumpSummary(d: DumpStore.Dump): String =
        "${d.size shr 20} MB" + if (d.os.isNotEmpty()) ", OS ${d.os}" else ""

    /**
     * Once per dump: the emulator is made and tested for OS 3.60. Another version is not
     * refused (a 3.80 OS boots, runs and installs add-ins in our tests; the core switches its
     * 3.60-only shortcuts off by itself), but the user should know.
     */
    private fun warnAboutOS(d: DumpStore.Dump) {
        if (d.osSupported) return
        val key = "os_warned_${d.hash}"
        if (prefs.getBoolean(key, false)) return
        prefs.edit().putBoolean(key, true).apply()
        val which = if (d.os.isNotEmpty()) getString(R.string.os_named, d.os) else getString(R.string.os_unknown)
        AlertDialog.Builder(this)
            .setTitle(R.string.os_warning_title)
            .setMessage(getString(R.string.os_warning_message, store.nameOf(d), which, DumpStore.SUPPORTED_OS))
            .setPositiveButton(android.R.string.ok, null)
            .show()
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

    /**
     * Settings (long-press the screen), in sections: speed — original hardware (authentic) vs
     * fast (the SH7305's full clock, about 2x), applied at once; the flash dump; installing an
     * add-in; the launcher icon.
     */
    private fun showSettings() {
        val v = DialogSettingsBinding.inflate(layoutInflater)
        v.speedGroup.check(if (fast) R.id.speedFast else R.id.speedOriginal)
        v.speedGroup.setOnCheckedChangeListener { _, id ->
            prefs.edit().putString(PREF_SPEED, if (id == R.id.speedFast) "fast" else "original").apply()
            applySpeed()
        }
        dump?.let { v.dumpInfo.text = getString(R.string.settings_dump_in_use, store.nameOf(it), dumpSummary(it)) }
        val icon = AppIcons.current(this)
        v.appIconImage.setImageResource(icon.mipmap)
        v.appIconName.setText(icon.label)
        val dialog = AlertDialog.Builder(this)
            .setTitle(R.string.settings_title)
            .setView(v.root)
            .setPositiveButton(R.string.settings_done, null)
            .show()
        v.changeDump.setOnClickListener { dialog.dismiss(); showChangeDump() }
        v.installAddin.setOnClickListener { dialog.dismiss(); pickAddin.launch(arrayOf("*/*")) }
        v.appIcon.setOnClickListener { dialog.dismiss(); showAppIcons() }
    }

    /**
     * Settings → App icon…: the three launcher icons, the current one marked (AppIcons).
     * Disabling the alias the app was started from makes Android close the app's task (the
     * process stays: DONT_KILL_APP), so the session is saved first and the dialog says so; the
     * app is opened again from the new icon and resumes where it was.
     */
    private fun showAppIcons() {
        if (NativeBridge.busy) return toast(getString(R.string.install_busy))
        val current = AppIcons.current(this)
        val v = DialogAppIconBinding.inflate(layoutInflater)
        val dialog = AlertDialog.Builder(this)
            .setTitle(R.string.app_icon_title)
            .setView(v.root)
            .setNegativeButton(android.R.string.cancel, null)
            .create()
        for (icon in AppIcons.all) {
            val row = ItemAppIconBinding.inflate(layoutInflater, v.icons, true)
            row.iconImage.setImageResource(icon.mipmap)
            row.iconName.setText(icon.label)
            row.iconCurrent.isChecked = icon === current
            row.root.setOnClickListener {
                dialog.dismiss()
                if (icon === current) return@setOnClickListener
                Log.i(TAG, "app icon: ${icon.alias}")
                saveState()
                toast(getString(R.string.app_icon_changed, getString(icon.label)))
                AppIcons.select(this, icon)
            }
        }
        dialog.show()
    }

    // ---- the flash dump: first start, choosing and changing it ----

    /** The first-start screen (no usable dump); [error] says what was wrong with the last one. */
    private fun showWelcome(error: String?) {
        binding.calc.visibility = View.GONE
        binding.welcome.visibility = View.VISIBLE
        showWelcomeError(error)
    }

    private fun showWelcomeError(error: String?) {
        binding.welcomeError.text = error ?: ""
        binding.welcomeError.visibility = if (error == null) View.GONE else View.VISIBLE
    }

    private fun launchDumpPicker() {
        if (importing) return
        // Any type: a .bin has no registered MIME type, so most providers call it octet-stream
        // or nothing at all; DumpStore checks the contents.
        pickDump.launch(arrayOf("*/*"))
    }

    /** Settings → Change flash dump…: what is in use, then the picker. */
    private fun showChangeDump() {
        val d = dump ?: return launchDumpPicker()
        AlertDialog.Builder(this)
            .setTitle(R.string.change_dump_title)
            .setMessage(getString(R.string.change_dump_message, store.nameOf(d), dumpSummary(d), d.hash))
            .setPositiveButton(R.string.change_dump_choose) { _, _ ->
                if (NativeBridge.busy) toast(getString(R.string.dump_busy)) else launchDumpPicker()
            }
            .setNegativeButton(android.R.string.cancel, null)
            .show()
    }

    /**
     * Checks and copies the chosen file on a worker thread (up to 32 MB), then restarts the
     * activity on it: onPause saves the running dump's session first, and onCreate loads the
     * new dump and resumes its own session (or cold-boots).
     */
    private fun importDump(uri: Uri) {
        if (importing) return
        if (NativeBridge.busy) {
            toast(getString(R.string.dump_busy))
            return
        }
        importing = true
        val name = store.displayName(uri)
        val busyText = if (name != null) getString(R.string.dump_copying, name) else getString(R.string.dump_copying_generic)
        if (running) {
            binding.statusText.text = busyText
        } else {
            showWelcomeError(null)
            binding.welcomeProgress.visibility = View.VISIBLE
            binding.chooseDump.isEnabled = false
            binding.chooseDump.text = busyText
        }
        val current = dump?.hash
        Thread {
            val problem = store.import(uri, current)
            Log.i(TAG, "import $uri ($name): ${problem ?: "ok"}")
            runOnUiThread {
                importing = false
                if (isFinishing || isDestroyed) return@runOnUiThread
                if (problem == null) {
                    recreate()
                    return@runOnUiThread
                }
                val msg = problemText(problem)
                if (running) {
                    binding.statusText.text = getString(R.string.hint_settings)
                    if (problem is DumpStore.Problem.SameDump) {
                        toast(msg)
                    } else {
                        AlertDialog.Builder(this)
                            .setTitle(R.string.dump_rejected_title)
                            .setMessage(msg)
                            .setPositiveButton(R.string.change_dump_choose) { _, _ -> launchDumpPicker() }
                            .setNegativeButton(android.R.string.cancel, null)
                            .show()
                    }
                } else {
                    binding.welcomeProgress.visibility = View.GONE
                    binding.chooseDump.isEnabled = true
                    binding.chooseDump.setText(R.string.choose_dump)
                    showWelcomeError(msg)
                }
            }
        }.start()
    }

    private fun problemText(p: DumpStore.Problem): String = when (p) {
        is DumpStore.Problem.WrongSize -> getString(R.string.dump_wrong_size, p.name, sizeText(p.size))
        is DumpStore.Problem.NotCG50 -> getString(R.string.dump_not_cg50, p.name)
        is DumpStore.Problem.Unreadable -> getString(R.string.dump_unreadable, p.name, p.reason)
        is DumpStore.Problem.SameDump -> getString(R.string.dump_same, p.name)
    }

    private fun sizeText(n: Long): String = when {
        n >= 1 shl 20 -> "%.1f MB (%d bytes)".format(n / 1048576.0, n)
        n >= 1 shl 10 -> "%.1f KB (%d bytes)".format(n / 1024.0, n)
        else -> "$n bytes"
    }

    /** A .g3a opened with / shared to the app, or `--es install NAME` (a file in our files dir). */
    private fun handleInstallIntent(intent: Intent) {
        intent.getStringExtra("install")?.let { name ->
            val f = File(store.dir, name)
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
        var name = store.displayName(uri) ?: uri.lastPathSegment?.substringAfterLast('/') ?: "addin.g3a"
        if (!name.endsWith(".g3a", ignoreCase = true)) name += ".g3a" // the core checks the header
        val data = try {
            contentResolver.openInputStream(uri)?.use { it.readBytes() }
        } catch (e: Exception) {
            Log.w(TAG, "read $uri: $e")
            null
        }
        if (data == null) toast(getString(R.string.install_unreadable, name)) else install(name, data)
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
        if (!running) return
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
