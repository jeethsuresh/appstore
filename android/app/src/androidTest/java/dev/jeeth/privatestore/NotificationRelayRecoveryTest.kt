package dev.jeeth.privatestore

import android.content.ContentProvider
import android.content.ContentValues
import android.database.Cursor
import android.net.Uri
import android.os.Bundle
import androidx.test.core.app.ActivityScenario
import androidx.test.ext.junit.runners.AndroidJUnit4
import androidx.test.platform.app.InstrumentationRegistry
import org.json.JSONObject
import org.junit.Assert.*
import org.junit.Assume.assumeTrue
import org.junit.Test
import org.junit.runner.RunWith
import java.net.InetAddress
import java.net.Socket
import java.security.KeyStore
import java.security.MessageDigest
import java.util.concurrent.ConcurrentHashMap
import java.util.concurrent.Executors
import java.util.concurrent.atomic.AtomicInteger
import javax.net.ssl.KeyManagerFactory
import javax.net.ssl.SSLContext
import javax.net.ssl.SSLServerSocket

/** Installed only in the disposable instrumentation APK. Never shipped. */
class RelayFixtureProvider : ContentProvider() {
    override fun onCreate() = true
    @Synchronized override fun call(method: String, arg: String?, extras: Bundle?): Bundle {
        val p = requireNotNull(context).getSharedPreferences("relay-fixture", 0)
        return when (method) {
            "reset" -> { p.edit().clear().putBoolean("failFirst", extras?.getBoolean("failFirst") == true).commit(); Bundle() }
            "notify" -> {
                val id = requireNotNull(extras).getString("id")!!
                val attempts = p.getInt("attempts:$id", 0) + 1
                p.edit().putInt("attempts:$id", attempts).commit()
                if (id == "1" && attempts == 1 && p.getBoolean("failFirst", false)) throw IllegalArgumentException("Temporary fixture provider failure")
                p.edit().putBoolean("accepted:$id", true).commit()
                Bundle().apply { putBoolean("accepted", true) }
            }
            "status" -> Bundle().apply {
                putBoolean("accepted", p.getBoolean("accepted:$arg", false))
                putInt("attempts", p.getInt("attempts:$arg", 0))
            }
            else -> throw IllegalArgumentException("Unknown test operation")
        }
    }
    override fun query(u: Uri, p: Array<out String>?, s: String?, a: Array<out String>?, o: String?): Cursor? = throw UnsupportedOperationException()
    override fun getType(u: Uri): String? = null
    override fun insert(u: Uri, v: ContentValues?): Uri? = throw UnsupportedOperationException()
    override fun delete(u: Uri, s: String?, a: Array<out String>?): Int = throw UnsupportedOperationException()
    override fun update(u: Uri, v: ContentValues?, s: String?, a: Array<out String>?): Int = throw UnsupportedOperationException()
}

@RunWith(AndroidJUnit4::class)
class NotificationRelayRecoveryTest {
    private val instrumentation get() = InstrumentationRegistry.getInstrumentation()
    private val context get() = instrumentation.targetContext
    private val fixtureUri get() = Uri.parse("content://${instrumentation.context.packageName}.notifications")
    private fun status(id: String) = requireNotNull(context.contentResolver.call(fixtureUri, "status", id, null))
    private fun await(seconds: Long, description: String, test: () -> Boolean) {
        val deadline = System.nanoTime() + seconds * 1_000_000_000
        while (System.nanoTime() < deadline) { if (test()) return; Thread.sleep(50) }
        fail("Timed out: $description")
    }

    private class FixtureServer(context: android.content.Context, val firstStream: String) : AutoCloseable {
        val streams = AtomicInteger()
        val acknowledged = ConcurrentHashMap.newKeySet<String>()
        private val workers = Executors.newCachedThreadPool()
        private val sockets = ConcurrentHashMap.newKeySet<Socket>()
        private val server: SSLServerSocket
        @Volatile private var closed = false
        init {
            val keyStore = KeyStore.getInstance("PKCS12")
            val resource = context.resources.getIdentifier("notification_test_server", "raw", context.packageName)
            require(resource != 0) { "Generate the isolated notification TLS fixture before running" }
            context.resources.openRawResource(resource).use { keyStore.load(it, "fixture".toCharArray()) }
            val keys = KeyManagerFactory.getInstance(KeyManagerFactory.getDefaultAlgorithm()).apply { init(keyStore, "fixture".toCharArray()) }
            val tls = SSLContext.getInstance("TLS").apply { init(keys.keyManagers, null, null) }
            server = tls.serverSocketFactory.createServerSocket(0, 20, InetAddress.getByName("127.0.0.1")) as SSLServerSocket
            workers.execute {
                while (!closed) try {
                    val socket = server.accept(); sockets.add(socket)
                    workers.execute { try { handle(socket) } catch (_: Exception) {} finally { sockets.remove(socket); socket.close() } }
                } catch (_: Exception) { if (!closed) throw IllegalStateException("Fixture listener failed") }
            }
        }
        val origin get() = "https://127.0.0.1:${server.localPort}"
        private fun handle(socket: Socket) {
            socket.soTimeout = 5000
            val reader = socket.getInputStream().bufferedReader()
            val line = reader.readLine() ?: return
            val path = line.split(" ")[1]
            var length = 0
            while (true) {
                val header = reader.readLine() ?: return
                if (header.isEmpty()) break
                if (header.startsWith("Content-Length:", true)) length = header.substringAfter(":").trim().toInt()
            }
            repeat(length) { check(reader.read() >= 0) }
            val output = socket.getOutputStream()
            if (path == "/v1/notifications/stream") {
                val number = streams.incrementAndGet()
                output.write("HTTP/1.1 200 OK\r\nContent-Type: application/x-ndjson\r\nConnection: close\r\n\r\n".toByteArray()); output.flush()
                if (number == 1 && firstStream == "drop") return
                if (number == 1 && firstStream == "silent") { Thread.sleep(115_000); return }
                while (!closed) {
                    for (id in listOf("1", "2")) if (!acknowledged.contains(id)) {
                        output.write((JSONObject().put("id", id.toLong()).put("app", "relayfixture").put("payload", JSONObject().put("kind", "fixture")).toString() + "\n").toByteArray())
                    }
                    output.flush(); Thread.sleep(250)
                }
            } else {
                if (path.startsWith("/v1/notifications/")) acknowledged.add(path.substringAfterLast('/'))
                output.write("HTTP/1.1 204 No Content\r\nContent-Length: 0\r\nConnection: close\r\n\r\n".toByteArray()); output.flush()
            }
        }
        override fun close() { closed = true; server.close(); sockets.forEach { it.close() }; workers.shutdownNow() }
    }

    private fun withRelay(firstStream: String = "normal", failFirst: Boolean = false, test: (FixtureServer) -> Unit) {
        assumeTrue(context.packageName.endsWith(".testbed"))
        NotificationRelay.clear(context, unregister = false)
        context.contentResolver.call(fixtureUri, "reset", null, Bundle().apply { putBoolean("failFirst", failFirst) })
        FixtureServer(context, firstStream).use { server ->
            ActivityScenario.launch(MainActivity::class.java).use {
                val pkg = instrumentation.context.packageName
                val info = context.packageManager.getPackageInfo(pkg, android.content.pm.PackageManager.GET_SIGNING_CERTIFICATES)
                val certificate = MessageDigest.getInstance("SHA-256").digest(info.signingInfo!!.apkContentsSigners.single().toByteArray()).joinToString("") { "%02x".format(it) }
                val app = StoreApp("relayfixture", "Fixture", pkg, certificate, "Disposable test receiver", null, "$pkg.notifications")
                try {
                    NotificationRelay.configure(context, Session(server.origin, "fixture-token", "fixture-account"), listOf(app), true)
                    test(server)
                } finally { NotificationRelay.clear(context, unregister = false) }
            }
        }
    }

    @Test fun failedReceiverIsRetriedWithoutBlockingOtherAlertsOrAcknowledgingFailure() = withRelay(failFirst = true) { server ->
        await(15, "both alerts acknowledged after app acceptance") { server.acknowledged.containsAll(listOf("1", "2")) }
        assertTrue("Failed Binder call must be retried before acknowledgement", status("1").getInt("attempts") >= 2)
        assertTrue(status("1").getBoolean("accepted")); assertTrue(status("2").getBoolean("accepted"))
    }

    @Test fun interruptedStreamReconnectsWithoutOpeningStoreAgain() = withRelay(firstStream = "drop") { server ->
        await(15, "automatic reconnect and queued replay") { server.streams.get() >= 2 && server.acknowledged.contains("2") }
        assertTrue(status("2").getBoolean("accepted"))
    }

    @Test fun reopeningStoreReplacesASilentlyDeadStream() = withRelay(firstStream = "silent") { server ->
        await(10, "first silent connection") { server.streams.get() == 1 }
        NotificationRelay.start(context)
        await(10, "explicit start replaces dead connection") { server.streams.get() >= 2 && server.acknowledged.contains("1") }
    }

    @Test fun aSilentlyDeadStreamHasABoundedAutomaticRecovery() = withRelay(firstStream = "silent") { server ->
        await(110, "read deadline, automatic reconnect and replay") { server.streams.get() >= 2 && server.acknowledged.contains("1") }
        assertTrue(status("1").getBoolean("accepted"))
    }
}
