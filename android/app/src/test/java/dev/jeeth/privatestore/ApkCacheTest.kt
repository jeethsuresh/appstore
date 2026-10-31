package dev.jeeth.privatestore

import org.junit.Assert.*
import org.junit.Test
import java.nio.file.Files
import java.security.MessageDigest
import java.util.concurrent.CountDownLatch
import java.util.concurrent.Executors
import java.util.concurrent.TimeUnit

class ApkCacheTest {
    private val bytes = "verified APK fixture".toByteArray()
    private val digest = MessageDigest.getInstance("SHA-256").digest(bytes).toHex()
    private val key = ApkCache.Key("dev.example.app", 2, digest)
    @Test fun retriesReuseVerifiedBytesAndSuccessfulInstallRemovesOnlyFinishedVersions() {
        val dir = Files.createTempDirectory("apk-cache").toFile()
        try {
            val cache = ApkCache(dir)
            val file = cache.obtain(key, bytes.size.toLong()) { it.writeBytes(bytes) }
            assertEquals(file, ApkCache(dir).obtain(key, bytes.size.toLong()) { fail("Retry must reuse saved download") })
            val future = ApkCache.Key(key.packageName, 3, digest)
            val futureFile = cache.obtain(future, bytes.size.toLong()) { it.writeBytes(bytes) }
            cache.complete(key.packageName, 1); assertTrue(file.exists())
            cache.complete(key.packageName, 2); assertFalse(file.exists()); assertTrue(futureFile.exists())
            cache.complete(key.packageName, 3); assertFalse(futureFile.exists())
        } finally { dir.deleteRecursively() }
    }
    @Test fun corruptedCacheAndInterruptedDownloadsAreNeverReused() {
        val dir = Files.createTempDirectory("apk-cache").toFile()
        try {
            val cache = ApkCache(dir)
            val file = cache.obtain(key, bytes.size.toLong()) { it.writeBytes(bytes) }
            file.writeBytes(ByteArray(bytes.size))
            var fetched = false
            cache.obtain(key, bytes.size.toLong()) { fetched = true; it.writeBytes(bytes) }
            assertTrue(fetched)
            file.delete()
            assertThrows(IllegalStateException::class.java) { cache.obtain(key, bytes.size.toLong()) { it.writeBytes(bytes); error("network lost") } }
            assertTrue(dir.listFiles()!!.isEmpty())
            assertThrows(IllegalArgumentException::class.java) { cache.obtain(key, bytes.size.toLong()) { it.writeBytes(ByteArray(bytes.size)) } }
            assertTrue(dir.listFiles()!!.isEmpty())
            cache.obtain(key, bytes.size.toLong()) { it.writeBytes(bytes) }
        } finally { dir.deleteRecursively() }
    }
    @Test fun concurrentRequestsForSameReleaseFetchOnce() {
        val dir = Files.createTempDirectory("apk-cache").toFile()
        val pool = Executors.newFixedThreadPool(3)
        try {
            val start = CountDownLatch(1)
            val fetches = java.util.concurrent.atomic.AtomicInteger()
            val results = (1..3).map { pool.submit<java.io.File> { start.await(); ApkCache(dir).obtain(key, bytes.size.toLong()) { fetches.incrementAndGet(); it.writeBytes(bytes) } } }
            start.countDown()
            assertEquals(1, results.map { it.get(5, TimeUnit.SECONDS) }.distinct().size)
            assertEquals(1, fetches.get())
        } finally { pool.shutdownNow(); dir.deleteRecursively() }
    }
    @Test fun rejectsInvalidMetadataAndPathTraversal() {
        assertThrows(IllegalArgumentException::class.java) { ApkCache.Key("../../outside", 1, digest) }
        assertThrows(IllegalArgumentException::class.java) { ApkCache.Key(key.packageName, 0, digest) }
        assertThrows(IllegalArgumentException::class.java) { ApkCache.Key(key.packageName, 1, "bad") }
    }
}
