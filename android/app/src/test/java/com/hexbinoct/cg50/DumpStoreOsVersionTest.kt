package com.hexbinoct.cg50

import org.junit.Assert.assertEquals
import org.junit.Test

/** DumpStore.osVersion must read what emu_go/osversion.go reads (same cases as its test). */
class DumpStoreOsVersionTest {

    private fun fakeFlash(version: String, header: String = "CASIOWIN"): ByteArray {
        val b = ByteArray(0x30000) { 0xFF.toByte() }
        header.toByteArray(Charsets.US_ASCII).copyInto(b, 0x20000)
        version.toByteArray(Charsets.US_ASCII).copyInto(b, 0x20020)
        return b
    }

    @Test
    fun readsTheVersion() {
        assertEquals("3.60", DumpStore.osVersion(fakeFlash("03.60.0000")))
        assertEquals("3.60", DumpStore.osVersion(fakeFlash("03.60.0202")))
        assertEquals("3.80", DumpStore.osVersion(fakeFlash("03.80.0000")))
        assertEquals("", DumpStore.osVersion(fakeFlash("garbage!!!")))
        assertEquals("", DumpStore.osVersion(fakeFlash("03.60.0000", header = "NOTCASIO")))
        assertEquals("", DumpStore.osVersion(ByteArray(0x100)))
    }
}
