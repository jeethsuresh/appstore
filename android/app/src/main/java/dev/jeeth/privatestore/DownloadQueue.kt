package dev.jeeth.privatestore

import kotlinx.coroutines.sync.Semaphore
import kotlinx.coroutines.sync.withPermit

/** Bounds network work across foreground and background callers, and deduplicates packages. */
class DownloadQueue(parallelism: Int) {
    private val slots = Semaphore(parallelism)
    private val running = mutableSetOf<String>()
    fun hasOther(packageName: String) = synchronized(running) { running.any { it != packageName } }
    suspend fun run(packageName: String, queued: () -> Unit = {}, action: suspend () -> Unit): Boolean {
        synchronized(running) { if (!running.add(packageName)) return false }
        try { queued(); slots.withPermit { action() }; return true }
        finally { synchronized(running) { running.remove(packageName) } }
    }
}
