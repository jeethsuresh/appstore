package dev.jeeth.privatestore

import java.util.Locale

/** Decimal download units, matching the web catalog and the verified APK byte count. */
object DownloadSizes {
    fun format(bytes: Long, locale: Locale = Locale.getDefault()): String = when {
        bytes < 1_000 -> "$bytes B"
        bytes < 1_000_000 -> String.format(locale, "%.1f kB", bytes / 1_000.0)
        else -> String.format(locale, "%.1f MB", bytes / 1_000_000.0)
    }
}
