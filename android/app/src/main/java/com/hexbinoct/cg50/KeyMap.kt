package com.hexbinoct.cg50

/**
 * On-screen keypad, laid out exactly like the fx-CG50's keyboard. Each Key carries its 0-based
 * matrix (row,col) — re/KEYMAP.md's `inject` column (EXE = "2-1" -> row=2,col=1) — which
 * NativeBridge.keyDown/keyUp set in the emulated key-scan unit, plus the legends printed around
 * it on the real faceplate: SHIFT function (yellow, above-left) and ALPHA character (red,
 * above-right). SHIFT/ALPHA are real keys: tap one before the target and the OS applies the
 * yellow/red meaning itself. `python re/audit_keymap.py` checks these labels against the codes
 * the OS actually produces for each matrix position.
 */
enum class Cap { FUNC, DARK, LIGHT, ACCENT }

data class Key(
    val label: String, val row: Int, val col: Int,
    val shift: String = "", val alpha: String = "", val cap: Cap = Cap.DARK,
)

/** F1..F6 (the small keys under the screen). */
val FKEYS = listOf(
    Key("F1", 6, 9, "Trace", cap = Cap.FUNC), Key("F2", 5, 9, "Zoom", cap = Cap.FUNC),
    Key("F3", 4, 9, "V-Window", cap = Cap.FUNC), Key("F4", 3, 9, "Sketch", cap = Cap.FUNC),
    Key("F5", 2, 9, "G-Solv", cap = Cap.FUNC), Key("F6", 1, 9, "G↔T", cap = Cap.FUNC),
)

/** The two rows left of the D-pad. */
val TOP_ROWS = listOf(
    listOf(Key("SHIFT", 6, 8), Key("OPTN", 5, 8), Key("VARS", 4, 8, "PRGM"), Key("MENU", 3, 8, "SET UP")),
    listOf(Key("ALPHA", 6, 7, "A-LOCK"), Key("x²", 5, 7, "√", "r"), Key("^", 4, 7, "ˣ√", "θ"), Key("EXIT", 3, 7, "QUIT")),
)

/** D-pad: up, down, left, right. */
val DPAD = listOf(Key("▲", 1, 8), Key("▼", 2, 7), Key("◀", 2, 8), Key("▶", 1, 7))

/** The two six-key function rows. */
val FUNC_ROWS = listOf(
    listOf(
        Key("X,θ,T", 6, 6, "∠", "A"), Key("log", 5, 6, "10ˣ", "B"), Key("ln", 4, 6, "eˣ", "C"),
        Key("sin", 3, 6, "sin⁻¹", "D"), Key("cos", 2, 6, "cos⁻¹", "E"), Key("tan", 1, 6, "tan⁻¹", "F"),
    ),
    listOf(
        Key("a⁄b", 6, 5, "a b⁄c", "G"), Key("S⇔D", 5, 5, "F⇔D", "H"), Key("(", 4, 5, "∛", "I"),
        Key(")", 3, 5, "x⁻¹", "J"), Key(",", 2, 5, "", "K"), Key("→", 1, 5, "", "L"),
    ),
)

/** The four large rows of the number pad. */
val NUM_ROWS = listOf(
    listOf(
        Key("7", 6, 4, "CAPTURE", "M", Cap.LIGHT), Key("8", 5, 4, "CLIP", "N", Cap.LIGHT),
        Key("9", 4, 4, "PASTE", "O", Cap.LIGHT), Key("DEL", 3, 4, "INS", cap = Cap.ACCENT),
        Key("AC/ON", 0, 0, "OFF", cap = Cap.ACCENT),
    ),
    listOf(
        Key("4", 6, 3, "CATALOG", "P", Cap.LIGHT), Key("5", 5, 3, "FORMAT", "Q", Cap.LIGHT),
        Key("6", 4, 3, "", "R", Cap.LIGHT), Key("×", 3, 3, "{", "S", Cap.LIGHT), Key("÷", 2, 3, "}", "T", Cap.LIGHT),
    ),
    listOf(
        Key("1", 6, 2, "List", "U", Cap.LIGHT), Key("2", 5, 2, "Mat", "V", Cap.LIGHT),
        Key("3", 4, 2, "", "W", Cap.LIGHT), Key("+", 3, 2, "[", "X", Cap.LIGHT), Key("−", 2, 2, "]", "Y", Cap.LIGHT),
    ),
    listOf(
        Key("0", 6, 1, "i", "Z", Cap.LIGHT), Key(".", 5, 1, "=", "SPACE", Cap.LIGHT),
        Key("×10ˣ", 4, 1, "π", "\"", Cap.LIGHT), Key("(−)", 3, 1, "Ans", cap = Cap.LIGHT),
        Key("EXE", 2, 1, "↵", cap = Cap.LIGHT),
    ),
)
