package dev.jeeth.privatestore

import kotlinx.coroutines.*
import org.junit.Assert.*
import org.junit.Test
import java.util.concurrent.atomic.AtomicInteger

class DownloadQueueTest {
    @Test fun sixDownloadsOverlapAtMostThreeAndAllFinish() = runBlocking {
        val queue = DownloadQueue(3)
        val active = AtomicInteger(); val peak = AtomicInteger(); val finished = AtomicInteger()
        val firstThree = CompletableDeferred<Unit>(); val release = CompletableDeferred<Unit>()
        val jobs = (1..6).map { id -> async(Dispatchers.Default) {
            queue.run("app.$id") {
                val count = active.incrementAndGet(); peak.accumulateAndGet(count, ::maxOf)
                if (count == 3) firstThree.complete(Unit)
                release.await(); active.decrementAndGet(); finished.incrementAndGet()
            }
        } }
        withTimeout(5_000) { firstThree.await() }
        assertEquals(3, active.get()); assertTrue(queue.hasOther("app.1"))
        release.complete(Unit); withTimeout(5_000) { jobs.awaitAll() }
        assertEquals(3, peak.get()); assertEquals(6, finished.get()); assertFalse(queue.hasOther("app.1"))
    }
    @Test fun duplicateFailureAndCancellationReleaseTheirSlots() = runBlocking {
        val queue = DownloadQueue(1)
        val entered = CompletableDeferred<Unit>(); val hold = CompletableDeferred<Unit>()
        val first = launch { queue.run("one") { entered.complete(Unit); hold.await() } }
        entered.await()
        assertFalse(queue.run("one") { fail("Duplicate package must not run") })
        first.cancelAndJoin()
        assertFalse(queue.hasOther("different"))
        try { queue.run("broken") { error("download failed") }; fail("Expected failure") } catch (_: IllegalStateException) { }
        withTimeout(5_000) { assertTrue(queue.run("next") { }) }
    }
}
