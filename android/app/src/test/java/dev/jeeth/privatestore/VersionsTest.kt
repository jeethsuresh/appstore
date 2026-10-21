package dev.jeeth.privatestore
import org.junit.Assert.*
import org.junit.Test
class VersionsTest {
    @Test fun updatesUseVersionCodes() {
        assertTrue(Versions.newer(null, 2)); assertTrue(Versions.newer(1, 2))
        assertFalse(Versions.newer(2, 2)); assertFalse(Versions.newer(3, 2))
    }
    @Test fun serverCannotRedirectTokensToAnotherOrigin() {
        assertEquals("https://store.example.com", Versions.baseUrl(" https://store.example.com/ "))
        for (bad in listOf("http://store.example.com", "https://user@store.example.com", "https://store.example.com/path", "https://store.example.com?token=x", "https://store.example.com#fragment")) {
            assertTrue(runCatching { Versions.baseUrl(bad) }.isFailure)
        }
    }
    @Test fun digestsCannotBecomePaths() {
        assertTrue(Versions.validDigest("a".repeat(64)))
        assertFalse(Versions.validDigest("../apk")); assertFalse(Versions.validDigest("A".repeat(64)))
    }
}
