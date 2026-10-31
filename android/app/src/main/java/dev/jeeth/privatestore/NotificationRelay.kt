package dev.jeeth.privatestore

import android.app.*
import android.content.*
import android.content.pm.PackageManager
import android.content.pm.ServiceInfo
import android.database.Cursor
import android.net.*
import android.os.*
import android.security.keystore.KeyGenParameterSpec
import android.security.keystore.KeyProperties
import android.util.AtomicFile
import org.json.JSONArray
import org.json.JSONObject
import java.io.File
import java.net.HttpURLConnection
import java.net.URL
import java.security.KeyStore
import java.security.MessageDigest
import java.util.concurrent.Executors
import java.util.concurrent.ScheduledFuture
import java.util.concurrent.TimeUnit
import javax.crypto.Cipher
import javax.crypto.KeyGenerator
import javax.crypto.SecretKey
import javax.crypto.spec.GCMParameterSpec

/** Small, encrypted AtomicFile shared across processes; no SharedPreferences
 * cache or UI/account-broker initialization in the listener process. */
object NotificationRelay {
    fun ignoresBatteryOptimizations(c: Context): Boolean =
        c.getSystemService(PowerManager::class.java).isIgnoringBatteryOptimizations(c.packageName)

    fun canConnect(c: Context, networkValidated: Boolean): Boolean = NotificationRelayPolicy.canConnect(
        c.getSystemService(PowerManager::class.java).isDeviceIdleMode,
        ignoresBatteryOptimizations(c), networkValidated,
    )

    @Synchronized fun registrationGeneration(c: Context, advance: Boolean = false): Long {
        java.io.RandomAccessFile(File(c.filesDir, "notification-relay-generation"), "rw").use { file ->
            file.channel.lock().use {
                val previous = if (file.length() == 8L) { file.seek(0); file.readLong() } else 0L
                if (!advance) return previous
                val next = maxOf(System.currentTimeMillis(), previous + 1)
                file.seek(0); file.writeLong(next); file.fd.sync()
                return next
            }
        }
    }
    private fun file(c: Context) = AtomicFile(File(c.filesDir, "notification-relay"))
    private fun key(): SecretKey {
        val store = KeyStore.getInstance("AndroidKeyStore").apply { load(null) }
        (store.getKey("notification-relay", null) as? SecretKey)?.let { return it }
        return KeyGenerator.getInstance("AES", "AndroidKeyStore").apply {
            init(KeyGenParameterSpec.Builder("notification-relay", KeyProperties.PURPOSE_ENCRYPT or KeyProperties.PURPOSE_DECRYPT)
                .setBlockModes(KeyProperties.BLOCK_MODE_GCM).setEncryptionPaddings(KeyProperties.ENCRYPTION_PADDING_NONE).build())
        }.generateKey()
    }
    fun read(c: Context): JSONObject? = try {
        val bytes = file(c).openRead().use { it.readBytes() }
        require(bytes.size in 29..65536)
        val cipher = Cipher.getInstance("AES/GCM/NoPadding")
        cipher.init(Cipher.DECRYPT_MODE, key(), GCMParameterSpec(128, bytes.copyOfRange(0, 12)))
        JSONObject(String(cipher.doFinal(bytes.copyOfRange(12, bytes.size)), Charsets.UTF_8))
    } catch (_: Exception) { null }
    fun configure(c: Context, session: Session, apps: List<StoreApp>, enabled: Boolean) {
        if (!enabled || session.account.isBlank()) {
            clear(c)
            return
        }
        val targets = JSONArray()
        apps.filter { it.notificationAuthority != null }.forEach { app ->
            targets.put(JSONObject().put("id", app.id).put("package", app.packageName).put("certificate", app.certificate).put("authority", app.notificationAuthority))
        }
        val config = JSONObject().put("baseUrl", session.baseUrl).put("token", session.token).put("targets", targets)
        val previous = read(c)
        val comparable = previous?.let { JSONObject(it.toString()).apply { remove("generation") } }
        if (comparable?.toString() != config.toString()) {
            config.put("generation", registrationGeneration(c, advance = true))
            val cipher = Cipher.getInstance("AES/GCM/NoPadding").apply { init(Cipher.ENCRYPT_MODE, key()) }
            val atomic = file(c); val output = atomic.startWrite()
            try { output.write(cipher.iv); output.write(cipher.doFinal(config.toString().toByteArray())); atomic.finishWrite(output) }
            catch (e: Exception) { atomic.failWrite(output); throw e }
        }
        else config.put("generation", requireNotNull(previous).getLong("generation"))
        if (targets.length() > 0 && (0 until targets.length()).any { installed(c, targets.getJSONObject(it)) }) start(c)
        else {
            c.stopService(Intent(c, NotificationRelayService::class.java))
            Thread { runCatching { register(c, config, emptyList()) } }.start()
        }
    }
    fun clear(c: Context, unregister: Boolean = true) {
        val old = read(c)
        if (unregister && old != null) old.put("generation", registrationGeneration(c, advance = true))
        file(c).delete(); c.stopService(Intent(c, NotificationRelayService::class.java))
        if (unregister && old != null) {
            Thread { runCatching { register(c, old, emptyList()) } }.start()
        }
    }
    fun start(c: Context) {
        if (read(c) != null) runCatching { c.startForegroundService(Intent(c, NotificationRelayService::class.java)) }
    }
    fun targets(c: Context, config: JSONObject): List<JSONObject> {
        val array = config.getJSONArray("targets")
        return (0 until array.length()).map { array.getJSONObject(it) }.filter { installed(c, it) }
    }
    fun register(c: Context, config: JSONObject, targets: List<JSONObject> = targets(c, config)) {
        val conn = (URL(Versions.baseUrl(config.getString("baseUrl")) + "/v1/notification-apps").openConnection() as HttpURLConnection)
        try {
            conn.instanceFollowRedirects = false; conn.requestMethod = "PUT"; conn.connectTimeout = 3000; conn.readTimeout = 5000
            conn.setRequestProperty("Authorization", "Bearer ${config.getString("token")}")
            conn.setRequestProperty("Content-Type", "application/json"); conn.doOutput = true
            conn.outputStream.use { it.write(JSONObject().put("generation", config.getLong("generation")).put("apps", JSONArray(targets.map { it.getString("id") })).toString().toByteArray()) }
            require(conn.responseCode == 204)
        } finally { conn.disconnect() }
    }
    @Suppress("DEPRECATION")
    fun installed(c: Context, target: JSONObject): Boolean = try {
        val pkg = target.getString("package")
        val info = c.packageManager.getPackageInfo(pkg, if (Build.VERSION.SDK_INT >= 28) PackageManager.GET_SIGNING_CERTIFICATES else PackageManager.GET_SIGNATURES)
        val signers = if (Build.VERSION.SDK_INT >= 28) info.signingInfo?.apkContentsSigners else info.signatures
        val provider = c.packageManager.resolveContentProvider(target.getString("authority"), 0)
        target.getString("authority") == "$pkg.notifications" && provider?.packageName == pkg && provider.exported && signers?.size == 1 &&
            MessageDigest.getInstance("SHA-256").digest(signers[0].toByteArray()).toHex() == target.getString("certificate")
    } catch (_: Exception) { false }
}

/** One blocking socket/thread, no wake lock, client heartbeat, polling, alarms,
 * UI or WorkManager. Doze networking requires a user-granted power exemption. */
class NotificationRelayService : Service() {
    companion object { @Volatile var active: NotificationRelayService? = null }
    private val worker = Executors.newSingleThreadScheduledExecutor()
    private val main = Handler(Looper.getMainLooper())
    private lateinit var networks: ConnectivityManager
    @Volatile private var connection: HttpURLConnection? = null
    @Volatile private var stopped = false
    @Volatile private var generation = 0
    private var retry: ScheduledFuture<*>? = null
    private var failures = 0
    private var configuration: String? = null
    private var batteryExempt = false
    private val network = object : ConnectivityManager.NetworkCallback() {
        override fun onAvailable(network: Network) { main.post { reconnect() } }
        override fun onLost(network: Network) { main.post { reconnect() } }
        override fun onCapabilitiesChanged(n: Network, caps: NetworkCapabilities) { main.post { if (connection == null && caps.hasCapability(NetworkCapabilities.NET_CAPABILITY_VALIDATED)) reconnect() } }
    }
    private val idle = object : BroadcastReceiver() {
        override fun onReceive(c: Context, i: Intent) {
            // Keep an exempt connection alive when the screen-off device enters
            // Doze; only reconnect if the power policy blocks it or it is absent.
            if (!connectionAllowed() || connection == null) reconnect()
        }
    }
    private val packages = object : BroadcastReceiver() {
        override fun onReceive(c: Context, i: Intent) {
            if (i.getBooleanExtra(Intent.EXTRA_REPLACING, false) && i.action != Intent.ACTION_PACKAGE_REPLACED) return
            val config = NotificationRelay.read(c) ?: return
            val targets = config.getJSONArray("targets")
            if ((0 until targets.length()).any { targets.getJSONObject(it).getString("package") == i.data?.schemeSpecificPart }) reconnect()
        }
    }
    override fun onCreate() {
        super.onCreate(); active = this
        networks = getSystemService(ConnectivityManager::class.java)
        val manager = getSystemService(NotificationManager::class.java)
        manager.createNotificationChannel(NotificationChannel("app-notification-relay", "App notification connection", NotificationManager.IMPORTANCE_LOW).apply { setSound(null, null); enableVibration(false); setShowBadge(false) })
        batteryExempt = NotificationRelay.ignoresBatteryOptimizations(this)
        val notice = notification()
        if (Build.VERSION.SDK_INT >= 34) startForeground(910, notice, ServiceInfo.FOREGROUND_SERVICE_TYPE_SPECIAL_USE) else startForeground(910, notice)
        networks.registerDefaultNetworkCallback(network)
        registerReceiver(idle, IntentFilter(PowerManager.ACTION_DEVICE_IDLE_MODE_CHANGED))
        registerReceiver(packages, IntentFilter().apply { addAction(Intent.ACTION_PACKAGE_ADDED); addAction(Intent.ACTION_PACKAGE_REMOVED); addAction(Intent.ACTION_PACKAGE_REPLACED); addDataScheme("package") })
    }
    override fun onStartCommand(intent: Intent?, flags: Int, startId: Int): Int {
        val config = NotificationRelay.read(this)?.toString()
        if (config == null) { stopSelf(); return START_NOT_STICKY }
        val exempt = NotificationRelay.ignoresBatteryOptimizations(this)
        val powerChanged = exempt != batteryExempt
        batteryExempt = exempt
        if (powerChanged) getSystemService(NotificationManager::class.java).notify(910, notification())
        if (config != configuration || connection == null || powerChanged) { configuration = config; reconnect() }
        return START_STICKY
    }
    private fun notification(): Notification {
        val open = PendingIntent.getActivity(this, 910, Intent(this, MainActivity::class.java), PendingIntent.FLAG_IMMUTABLE or PendingIntent.FLAG_UPDATE_CURRENT)
        return Notification.Builder(this, "app-notification-relay").setSmallIcon(R.drawable.ic_store_foreground).setContentTitle("App notifications enabled")
            .setContentText(if (batteryExempt) "One shared connection · allowed while phone is idle" else "One shared connection · may wait while phone is idle")
            .setContentIntent(open).setOngoing(true).setOnlyAlertOnce(true).setShowWhen(false).build()
    }
    fun reconnect() {
        if (stopped) return
        generation++; connection?.disconnect(); connection = null; retry?.cancel(false); retry = null
        if (!connectionAllowed()) return
        val current = generation
        retry = worker.schedule({ listen(current) }, 1, TimeUnit.SECONDS)
    }
    private fun connectionAllowed(): Boolean {
        val caps = networks.getNetworkCapabilities(networks.activeNetwork)
        return NotificationRelay.canConnect(this, caps?.hasCapability(NetworkCapabilities.NET_CAPABILITY_VALIDATED) == true)
    }
    private fun connect(config: JSONObject, path: String, method: String = "GET"): HttpURLConnection =
        (URL(Versions.baseUrl(config.getString("baseUrl")) + path).openConnection() as HttpURLConnection).apply {
            instanceFollowRedirects = false; requestMethod = method; connectTimeout = 20_000; readTimeout = 30_000
            setRequestProperty("Authorization", "Bearer ${config.getString("token")}")
        }
    private fun unauthorized(config: JSONObject, current: Int) {
        main.post {
            if (stopped || current != generation) return@post
            val saved = NotificationRelay.read(this)
            if (saved?.optLong("generation") == config.optLong("generation")) {
                stopped = true
                NotificationRelay.clear(this, unregister = false)
            } else {
                configuration = saved?.toString()
                if (saved == null) { stopped = true; stopSelf() } else reconnect()
            }
        }
    }
    private fun listen(current: Int) {
        var live: HttpURLConnection? = null
        var connectedAt = 0L
        try {
            if (current != generation || stopped) return
            val config = NotificationRelay.read(this) ?: return
            val targets = config.getJSONArray("targets")
            val installed = (0 until targets.length()).map { targets.getJSONObject(it) }.filter { NotificationRelay.installed(this, it) }
            val registration = connect(config, "/v1/notification-apps", "PUT")
            connection = registration
            try {
                registration.doOutput = true; registration.setRequestProperty("Content-Type", "application/json")
                registration.outputStream.use { it.write(JSONObject().put("generation", config.getLong("generation")).put("apps", JSONArray(installed.map { it.getString("id") })).toString().toByteArray()) }
                if (registration.responseCode == 401) { unauthorized(config, current); return }
                require(registration.responseCode == 204)
            } finally { registration.disconnect() }
            if (installed.isEmpty()) { stopped = true; stopSelf(); return }
            if (current != generation || stopped) return
            live = connect(config, "/v1/notifications/stream").apply { readTimeout = 20 * 60_000 }
            connection = live
            if (live.responseCode == 401) { unauthorized(config, current); return }
            require(live.responseCode == 200)
            connectedAt = SystemClock.elapsedRealtime()
            live.inputStream.bufferedReader().use { reader ->
                while (current == generation && !stopped) {
                    // Bound each frame rather than allocating readLine() on untrusted input.
                    val line = StringBuilder()
                    while (true) { val char = reader.read(); if (char == -1) throw java.io.EOFException(); if (char == 10) break; require(line.length < 16384); line.append(char.toChar()) }
                    if (line.isBlank()) continue
                    val message = JSONObject(line.toString())
                    if (NotificationRelay.read(this)?.toString() != config.toString() || current != generation) return
                    val target = installed.singleOrNull { it.getString("id") == message.getString("app") }
                    if (target != null && NotificationRelay.installed(this, target)) {
                        val result = try { contentResolver.call(Uri.parse("content://${target.getString("authority")}"), "notify", null,
                            Bundle().apply { putString("id", message.getLong("id").toString()); putString("payload", message.getJSONObject("payload").toString()) })
                        } catch (_: IllegalArgumentException) { Bundle().apply { putBoolean("accepted", true) } }
                        require(result?.getBoolean("accepted") == true)
                    }
                    val ack = connect(config, "/v1/notifications/${message.getLong("id")}", "DELETE")
                    connection = ack
                    try { require(ack.responseCode == 204) } finally { ack.disconnect(); connection = live }
                }
            }
        } catch (_: Exception) {
            // Backoff is a non-wakeup timer, cancelled offline or when Doze
            // blocks networking. A user-granted exemption also permits retries.
        } finally {
            live?.disconnect(); connection = null
            main.post {
                retry = null
                if (!stopped && current == generation && NotificationRelay.read(this) != null) {
                    if (connectionAllowed()) {
                        val connectedMillis = if (connectedAt == 0L) 0L else SystemClock.elapsedRealtime() - connectedAt
                        failures = NotificationRelayPolicy.afterDisconnect(failures, connectedMillis)
                        val delay = NotificationRelayPolicy.retryDelaySeconds(failures, java.util.Random().nextInt(30))
                        retry = worker.schedule({ listen(current) }, delay, TimeUnit.SECONDS)
                    }
                }
            }
        }
    }
    override fun onBind(intent: Intent?): IBinder? = null
    override fun onDestroy() {
        stopped = true; generation++; active = null; connection?.disconnect(); retry?.cancel(true); worker.shutdownNow()
        networks.unregisterNetworkCallback(network); unregisterReceiver(idle); unregisterReceiver(packages)
        super.onDestroy()
    }
}

/** Install/uninstall reconciliation runs once per package change. It never
 * launches an Activity or requests a battery-optimization exemption. */
class NotificationRelayReceiver : BroadcastReceiver() {
    override fun onReceive(context: Context, intent: Intent) {
        if (intent.action == Intent.ACTION_BOOT_COMPLETED || intent.action == Intent.ACTION_MY_PACKAGE_REPLACED) NotificationRelay.start(context)
        else {
            if (intent.getBooleanExtra(Intent.EXTRA_REPLACING, false)) return
            val config = NotificationRelay.read(context) ?: return
            val pkg = intent.data?.schemeSpecificPart ?: return
            val all = config.getJSONArray("targets")
            if ((0 until all.length()).none { all.getJSONObject(it).getString("package") == pkg }) return
            val running = NotificationRelayService.active
            if (running != null) { running.reconnect(); return }
            val pending = goAsync()
            Thread {
                try {
                    NotificationRelay.register(context, config)
                    if (NotificationRelay.targets(context, config).isNotEmpty()) NotificationRelay.start(context)
                } catch (_: Exception) { /* The next foreground start/update check reconciles again. */ }
                finally { pending.finish() }
            }.start()
        }
    }
}

/** A newly installed signed app can prepare distribution while it is visible.
 * Only its actual Binder UID and certificate are accepted. */
class NotificationRelayProvider : ContentProvider() {
    override fun onCreate() = true
    override fun call(method: String, arg: String?, extras: Bundle?): Bundle {
        val c = requireNotNull(context)
        if (method != "prepare" || arg != null || (extras != null && !extras.isEmpty)) throw SecurityException("Invalid relay request")
        val config = NotificationRelay.read(c) ?: return Bundle().apply { putBoolean("available", false) }
        val uid = Binder.getCallingUid()
        val packages = c.packageManager.getPackagesForUid(uid)?.toList().orEmpty()
        if (NotificationRelay.targets(c, config).none { packages == listOf(it.getString("package")) }) throw SecurityException("Unapproved notification client")
        NotificationRelay.start(c)
        return Bundle().apply { putBoolean("available", true) }
    }
    override fun query(uri: Uri, projection: Array<out String>?, selection: String?, selectionArgs: Array<out String>?, sortOrder: String?): Cursor? = throw UnsupportedOperationException()
    override fun getType(uri: Uri): String? = throw UnsupportedOperationException()
    override fun insert(uri: Uri, values: ContentValues?): Uri? = throw UnsupportedOperationException()
    override fun delete(uri: Uri, selection: String?, selectionArgs: Array<out String>?): Int = throw UnsupportedOperationException()
    override fun update(uri: Uri, values: ContentValues?, selection: String?, selectionArgs: Array<out String>?): Int = throw UnsupportedOperationException()
}

internal object NotificationRelayPolicy {
    fun canConnect(deviceIdle: Boolean, batteryExempt: Boolean, networkValidated: Boolean): Boolean =
        networkValidated && (!deviceIdle || batteryExempt)

    fun afterDisconnect(failures: Int, connectedMillis: Long): Int =
        if (connectedMillis >= 5 * 60_000L) 1 else (failures.coerceIn(0, 7) + 1).coerceAtMost(7)
    fun retryDelaySeconds(failures: Int, jitter: Int): Long =
        (30L shl failures.coerceIn(1, 7)).coerceAtMost(1800L) + jitter.coerceIn(0, 29)
}
