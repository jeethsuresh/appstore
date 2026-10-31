package dev.jeeth.privatestore

import java.io.File
import java.security.MessageDigest

/** Verified downloads live in private files, so Android cannot evict a pending update. */
class ApkCache(private val directory: File) {
    data class Key(val packageName: String, val version: Long, val digest: String) {
        init {
            require(packageName.matches(Regex("[A-Za-z][A-Za-z0-9_]*(\\.[A-Za-z0-9_]+)+")))
            require(version > 0 && Versions.validDigest(digest))
        }
        val filename get() = "$packageName.$version.$digest.apk"
    }
    fun obtain(key: Key, size: Long, fetch: (File) -> Unit): File = synchronized(locks[(key.hashCode() and Int.MAX_VALUE) % locks.size]) {
        require(size in 1..314_572_800L)
        check(directory.isDirectory || directory.mkdirs()) { "Cannot create download storage" }
        val destination = File(directory, key.filename)
        if (valid(destination, size, key.digest)) return@synchronized destination
        destination.delete()
        val temporary = File.createTempFile("download-", ".part", directory)
        try {
            fetch(temporary)
            require(valid(temporary, size, key.digest)) { "APK checksum or size mismatch" }
            check(temporary.renameTo(destination)) { "Cannot save APK" }
            destination
        } finally { temporary.delete() }
    }
    fun complete(packageName: String, installedVersion: Long) {
        directory.listFiles()?.forEach { file ->
            val match = filename.matchEntire(file.name) ?: return@forEach
            if (match.groupValues[1] == packageName && match.groupValues[2].toLongOrNull()?.let { it <= installedVersion } == true) file.delete()
        }
    }
    companion object {
        private val locks = Array(32) { Any() }
        private val filename = Regex("(.+)\\.([0-9]+)\\.([a-f0-9]{64})\\.apk")
        private fun valid(file: File, size: Long, digest: String): Boolean {
            if (!file.isFile || file.length() != size) return false
            val hash = MessageDigest.getInstance("SHA-256")
            file.inputStream().use { input -> val buffer = ByteArray(64 * 1024); while (true) { val count = input.read(buffer); if (count < 0) break; hash.update(buffer, 0, count) } }
            return hash.digest().toHex() == digest
        }
    }
}
