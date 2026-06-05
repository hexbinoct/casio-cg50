package com.hexbinoct.cg50

/**
 * On-screen keypad layout. Each Key carries its 0-based matrix (row,col) exactly as in
 * re/KEYMAP.md's `inject` column (e.g. EXE = "2-1" -> row=2,col=1), which NativeBridge.injectKey
 * feeds to the OS's own scan-enqueue routine. SHIFT/ALPHA are real keys: tap them before the
 * target and the OS applies the yellow/red meaning (it runs its own one-shot modifier state).
 */
data class Key(val label: String, val row: Int, val col: Int)

// Rows are laid out top-to-bottom; buttons share each row's width evenly.
val KEYPAD: List<List<Key>> = listOf(
    listOf(Key("F1", 6, 9), Key("F2", 5, 9), Key("F3", 4, 9), Key("F4", 3, 9), Key("F5", 2, 9), Key("F6", 1, 9)),
    listOf(Key("SHIFT", 6, 8), Key("ALPHA", 6, 7), Key("MENU", 3, 7), Key("◀", 2, 8), Key("▲", 1, 8), Key("▶", 1, 7)),
    listOf(Key("OPTN", 5, 8), Key("EXIT", 3, 8), Key("▼", 2, 7), Key("DEL", 3, 4), Key("(", 4, 5), Key(")", 3, 5)),
    listOf(Key("sin", 3, 6), Key("cos", 2, 6), Key("tan", 1, 6), Key("log", 5, 6), Key("ln", 4, 6), Key("X,θ,T", 6, 6)),
    listOf(Key("x²", 5, 7), Key("^", 4, 7), Key(",", 2, 5), Key("(-)", 3, 1), Key("×10ˣ", 4, 1), Key("S⇔D", 5, 5)),
    listOf(Key("7", 6, 4), Key("8", 5, 4), Key("9", 4, 4), Key("×", 3, 3), Key("÷", 2, 3)),
    listOf(Key("4", 6, 3), Key("5", 5, 3), Key("6", 4, 3), Key("+", 3, 2), Key("−", 2, 2)),
    listOf(Key("1", 6, 2), Key("2", 5, 2), Key("3", 4, 2), Key("0", 6, 1), Key(".", 5, 1)),
    listOf(Key("EXE", 2, 1)),
)
