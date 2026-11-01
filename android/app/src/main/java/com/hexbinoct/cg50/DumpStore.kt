package com.hexbinoct.cg50

import android.content.Context
import android.net.Uri
import android.provider.OpenableColumns
import java.io.File
import java.security.MessageDigest

/**
 * The user's flash dump and the save-states that belong to it, in the app's external files dir
 * (/sdcard/Android/data/com.hexbinoct.cg50/files/, so adb can still push files there):
 *
 *     flash_dump.bin          the dump chosen in the app (copied from the file picker)
 *     flash_full_32mb.bin     a dump pushed with adb (used when none was chosen in the app)
 *     flash_full.bin          the older 16 MB pushed dump (used when neither of the above exists)
 *     sessions/<hash>.state   where each dump was left, resumed on the next start
 *
 * <hash> is the first 16 hex digits of the SHA-256 of the dump, as in the desktop app
 * (app/store.go), so a save-state is never resumed on a different dump. The old fixed state
 * names next to a pushed dump (cg50_state_32mb.bin for flash_full_32mb.bin, cg50_state.bin for
 * flash_full.bin) are taken over into sessions/ on the next start, so an existing install keeps
 * its session and a freshly pushed provisioned state still takes effect.
 *
 * The app ships no firmware: a dump only ever comes from the user.
 */
class DumpStore(context: Context) {

    val dir: File = context.getExternalFilesDir(null) ?: context.filesDir
    private val prefs = context.getSharedPreferences("cg50", Context.MODE_PRIVATE)
    private val resolver = context.contentResolver

    companion object {
        const val PICKED = "flash_dump.bin"
        const val PUSHED_32 = "flash_full_32mb.bin"
        const val PUSHED_16 = "flash_full.bin"
        private const val PREF_DUMP = "dump"
        private const val PREF_DUMP_NAME = "dump_name" // the chosen file's own name
        const val SIZE_16 = 16 shl 20
        const val SIZE_32 = 32 shl 20
        /** The boot code's header carries "CASIO" in its first KB (offset 824 in the 3.60 dump). */
        private const val HEADER_SCAN = 0x1000
        private val MAGIC = "CASIO".toByteArray(Charsets.US_ASCII)
        /** Old, dump-independent state names, taken over into sessions/ (see the class doc). */
        private val LEGACY_STATE = mapOf(PUSHED_32 to "cg50_state_32mb.bin", PUSHED_16 to "cg50_state.bin")

        fun isDumpSize(n: Long) = n == SIZE_16.toLong() || n == SIZE_32.toLong()

        /** True if the first 4 KB contain "CASIO" (the same rule as app/store.go readDump). */
        fun hasHeader(b: ByteArray, n: Int = b.size): Boolean {
            val end = minOf(n, HEADER_SCAN) - MAGIC.size
            outer@ for (i in 0..end) {
                for (j in MAGIC.indices) if (b[i + j] != MAGIC[j]) continue@outer
                return true
            }
            return false
        }

        fun hashOf(b: ByteArray): String = hex(MessageDigest.getInstance("SHA-256").digest(b))
        private fun hex(d: ByteArray) = d.take(8).joinToString("") { "%02x".format(it) }
    }

    /** Why a file is not a flash dump; [name] is shown to the user. */
    sealed class Problem {
        data class WrongSize(val name: String, val size: Long) : Problem()
        data class NotCG50(val name: String) : Problem()
        data class Unreadable(val name: String, val reason: String) : Problem()
        data class SameDump(val name: String) : Problem()
    }

    /** A dump: its file, size and hash. */
    class Dump(val file: File, val size: Int, val hash: String)

    /** What [load] found: the dump and its bytes, or why it can't be used. */
    class Loaded(val dump: Dump?, val bytes: ByteArray?, val problem: Problem?)

    /** The dump the app runs: the one chosen in the app, else a pushed one; null = none yet. */
    fun activeFile(): File? {
        prefs.getString(PREF_DUMP, null)?.let { File(dir, it) }?.takeIf { it.isFile }?.let { return it }
        return listOf(PUSHED_32, PUSHED_16).map { File(dir, it) }.firstOrNull { it.isFile }
    }

    /** The name to show for a dump: the chosen file's own name, or the pushed file's. */
    fun nameOf(d: Dump): String =
        if (d.file.name == PICKED) prefs.getString(PREF_DUMP_NAME, null) ?: PICKED else d.file.name

    /** Reads and checks [f] (a pushed file is not checked by anyone else). */
    fun load(f: File): Loaded {
        val n = f.length()
        if (!isDumpSize(n)) return Loaded(null, null, Problem.WrongSize(f.name, n))
        val b = try {
            f.readBytes()
        } catch (e: Exception) {
            return Loaded(null, null, Problem.Unreadable(f.name, e.message ?: e.toString()))
        }
        if (!hasHeader(b)) return Loaded(null, null, Problem.NotCG50(f.name))
        return Loaded(Dump(f, b.size, hashOf(b)), b, null)
    }

    /** The session state of a dump; takes over an old fixed-name state of a pushed dump. */
    fun stateFile(d: Dump): File {
        val sessions = File(dir, "sessions").apply { mkdirs() }
        val state = File(sessions, "${d.hash}.state")
        LEGACY_STATE[d.file.name]?.let { File(dir, it) }?.takeIf { it.isFile }?.let { old ->
            if (!old.renameTo(state)) old.copyTo(state, overwrite = true).also { old.delete() }
        }
        return state
    }

    /**
     * Copies the file behind [uri] to flash_dump.bin if it is an fx-CG50 flash dump and makes it
     * the active one. Blocking (up to 32 MB of I/O): call it off the UI thread. [current] = the
     * hash of the running dump, to report choosing the same one again. Returns null when done.
     */
    fun import(uri: Uri, current: String?): Problem? {
        val name = displayName(uri) ?: uri.lastPathSegment?.substringAfterLast('/') ?: "file"
        querySize(uri)?.let { if (!isDumpSize(it)) return Problem.WrongSize(name, it) }
        val tmp = File(dir, "$PICKED.part")
        try {
            val md = MessageDigest.getInstance("SHA-256")
            val head = ByteArray(HEADER_SCAN)
            var total = 0L
            val input = resolver.openInputStream(uri) ?: return Problem.Unreadable(name, "no data")
            input.use { ins ->
                tmp.outputStream().use { out ->
                    val buf = ByteArray(1 shl 16)
                    while (true) {
                        val r = ins.read(buf)
                        if (r < 0) break
                        if (total < HEADER_SCAN) {
                            val k = minOf(r.toLong(), HEADER_SCAN - total).toInt()
                            System.arraycopy(buf, 0, head, total.toInt(), k)
                        }
                        total += r
                        if (total > SIZE_32) break // too big: no need to read the rest
                        if (total >= HEADER_SCAN && total - r < HEADER_SCAN && !hasHeader(head)) {
                            return Problem.NotCG50(name)
                        }
                        md.update(buf, 0, r)
                        out.write(buf, 0, r)
                    }
                }
            }
            if (!isDumpSize(total)) return Problem.WrongSize(name, total)
            val hash = hex(md.digest())
            if (hash == current) return Problem.SameDump(name)
            val dst = File(dir, PICKED)
            if (!tmp.renameTo(dst)) return Problem.Unreadable(name, "cannot write ${dst.path}")
            prefs.edit().putString(PREF_DUMP, PICKED).putString(PREF_DUMP_NAME, name).apply()
            return null
        } catch (e: Exception) {
            return Problem.Unreadable(name, e.message ?: e.toString())
        } finally {
            tmp.delete()
        }
    }

    private fun querySize(uri: Uri): Long? = try {
        resolver.query(uri, arrayOf(OpenableColumns.SIZE), null, null, null)?.use { c ->
            if (c.moveToFirst() && !c.isNull(0)) c.getLong(0) else null
        }
    } catch (_: Exception) {
        null
    }

    fun displayName(uri: Uri): String? = try {
        resolver.query(uri, arrayOf(OpenableColumns.DISPLAY_NAME), null, null, null)?.use { c ->
            if (c.moveToFirst()) c.getString(0) else null
        }
    } catch (_: Exception) {
        null
    }
}
