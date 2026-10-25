package dev.jeeth.privatestore

import java.util.Locale
import org.junit.Assert.assertEquals
import org.junit.Test

class DownloadSizesTest {
    @Test fun tinyApksKeepTheirActualSize() {
        assertEquals("11.6 kB", DownloadSizes.format(11_560, Locale.US))
    }

    @Test fun downloadUnitsCoverBytesKilobytesAndMegabytes() {
        for ((bytes, expected) in listOf(0L to "0 B", 1L to "1 B", 999L to "999 B",
                1_000L to "1.0 kB", 1_000_000L to "1.0 MB", 18_385_963L to "18.4 MB")) {
            assertEquals(expected, DownloadSizes.format(bytes, Locale.US))
        }
    }

    @Test fun decimalSeparatorUsesTheDisplayLocale() {
        assertEquals("11,6 kB", DownloadSizes.format(11_560, Locale.GERMANY))
    }
}
