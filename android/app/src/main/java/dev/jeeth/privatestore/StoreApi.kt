package dev.jeeth.privatestore

import android.content.Context
import android.security.keystore.KeyGenParameterSpec
import android.security.keystore.KeyProperties
import android.util.Base64
import org.json.JSONObject
import java.io.File
import java.net.HttpURLConnection
import java.net.URI
import java.net.URL
import java.security.KeyStore
import java.security.MessageDigest
import javax.crypto.Cipher
import javax.crypto.KeyGenerator
import javax.crypto.SecretKey
import javax.crypto.spec.GCMParameterSpec

data class Session(val baseUrl: String, val token: String, val account: String = "")
data class StoreRelease(val id: Long, val versionCode: Long, val versionName: String, val sha256: String, val size: Long, val minSdk: Int, val notes: String)
data class StoreApp(val id: String, val name: String, val packageName: String, val certificate: String, val description: String, val release: StoreRelease?)

object Versions {
    fun newer(installed: Long?, available: Long) = installed == null || available > installed
    fun validDigest(value: String) = value.matches(Regex("[a-f0-9]{64}"))
    fun baseUrl(value: String): String {
        val uri = URI(value.trim().trimEnd('/'))
        require(uri.scheme == "https" && !uri.host.isNullOrBlank() && uri.userInfo == null && uri.query == null && uri.fragment == null && (uri.path.isNullOrEmpty() || uri.path == "/")) { "Use an HTTPS server address without a path, such as https://store.example.com" }
        return uri.toString().trimEnd('/')
    }
}

// Only ciphertext is persisted. Android backup is disabled; the key never leaves AndroidKeyStore.
class SessionVault(context: Context) {
    private val prefs = context.getSharedPreferences("session", Context.MODE_PRIVATE)
    private fun key(): SecretKey {
        val ks = KeyStore.getInstance("AndroidKeyStore").apply { load(null) }
        (ks.getKey("private-store-session", null) as? SecretKey)?.let { return it }
        return KeyGenerator.getInstance(KeyProperties.KEY_ALGORITHM_AES, "AndroidKeyStore").apply {
            init(KeyGenParameterSpec.Builder("private-store-session", KeyProperties.PURPOSE_ENCRYPT or KeyProperties.PURPOSE_DECRYPT).setBlockModes(KeyProperties.BLOCK_MODE_GCM).setEncryptionPaddings(KeyProperties.ENCRYPTION_PADDING_NONE).build())
        }.generateKey()
    }
    fun save(session: Session) {
        val cipher = Cipher.getInstance("AES/GCM/NoPadding").apply { init(Cipher.ENCRYPT_MODE, key()) }
        val json = JSONObject().put("baseUrl", session.baseUrl).put("token", session.token).put("account", session.account).toString()
        val encrypted = cipher.doFinal(json.toByteArray(Charsets.UTF_8))
        check(prefs.edit().putString("iv", Base64.encodeToString(cipher.iv, Base64.NO_WRAP)).putString("data", Base64.encodeToString(encrypted, Base64.NO_WRAP)).commit())
    }
    fun load(): Session? {
        val data = prefs.getString("data", null) ?: return null
        val cipher = Cipher.getInstance("AES/GCM/NoPadding").apply { init(Cipher.DECRYPT_MODE, key(), GCMParameterSpec(128, Base64.decode(prefs.getString("iv", ""), Base64.NO_WRAP))) }
        val json = JSONObject(String(cipher.doFinal(Base64.decode(data, Base64.NO_WRAP)), Charsets.UTF_8))
        return Session(Versions.baseUrl(json.getString("baseUrl")), json.getString("token"), json.optString("account", ""))
    }
    private val pendingPrefs = context.getSharedPreferences("gravitas-pending", Context.MODE_PRIVATE)
    fun savePending(value: PendingLogin) {
        val cipher = Cipher.getInstance("AES/GCM/NoPadding").apply { init(Cipher.ENCRYPT_MODE, key()); updateAAD("gravitas-pending-v1".toByteArray()) }
        val json = JSONObject().put("baseUrl", value.baseUrl).put("state", value.state).put("verifier", value.verifier).put("deviceName", value.deviceName).put("expiresAt", value.expiresAt).put("authorizationUrl", value.authorizationUrl)
        val encrypted = cipher.doFinal(json.toString().toByteArray(Charsets.UTF_8))
        check(pendingPrefs.edit().putString("iv", Base64.encodeToString(cipher.iv, Base64.NO_WRAP)).putString("data", Base64.encodeToString(encrypted, Base64.NO_WRAP)).commit())
    }
    fun pending(): PendingLogin? {
        val data = pendingPrefs.getString("data", null) ?: return null
        val cipher = Cipher.getInstance("AES/GCM/NoPadding").apply { init(Cipher.DECRYPT_MODE, key(), GCMParameterSpec(128, Base64.decode(pendingPrefs.getString("iv", ""), Base64.NO_WRAP))); updateAAD("gravitas-pending-v1".toByteArray()) }
        val json = JSONObject(String(cipher.doFinal(Base64.decode(data, Base64.NO_WRAP)), Charsets.UTF_8))
        val value = PendingLogin(Versions.baseUrl(json.getString("baseUrl")), json.getString("state"), json.getString("verifier"), json.getString("deviceName"), json.getLong("expiresAt"), json.getString("authorizationUrl"))
        if (value.expiresAt <= System.currentTimeMillis()) { clearPending(); return null }
        return value
    }
    fun clearPending() { check(pendingPrefs.edit().clear().commit()) }
    fun clear() { prefs.edit().clear().commit(); clearPending() }
}
class ApiException(val code: Int, message: String) : Exception(message)
class StoreApi(private val session: Session) {
    private fun connection(path: String, method: String = "GET"): HttpURLConnection = (URL(session.baseUrl + path).openConnection() as HttpURLConnection).apply {
        connectTimeout = 20_000; readTimeout = 120_000; requestMethod = method
        instanceFollowRedirects = false
        setRequestProperty("Authorization", "Bearer ${session.token}")
    }
    private fun json(path: String, method: String = "GET", body: JSONObject? = null): JSONObject {
        val conn = connection(path, method)
        try {
            if (body != null) { conn.doOutput = true; conn.setRequestProperty("Content-Type", "application/json"); conn.outputStream.use { it.write(body.toString().toByteArray()) } }
            val code = conn.responseCode
            if (code !in 200..299) throw ApiException(code, when (code) { 401 -> if (path == "/v1/enroll") "Invitation is invalid, expired, or already used." else "Access expired or was revoked. Sign in with Gravitas again, or use a new invitation."; else -> "Server request failed ($code)." })
            if (code == 204) return JSONObject()
            return conn.inputStream.bufferedReader().use { reader ->
                val buffer = CharArray(4096); val result = StringBuilder()
                while (true) { val n = reader.read(buffer); if (n < 0) break; require(result.length + n <= 1_048_576) { "Server response is too large" }; result.append(buffer, 0, n) }
                JSONObject(result.toString())
            }
        } finally { conn.disconnect() }
    }
    fun enroll(invite: String, name: String): Session {
        val result = json("/v1/enroll", "POST", JSONObject().put("invite", invite.trim()).put("deviceName", name.trim()))
        return session.copy(token = result.getString("token"))
    }
    fun startLogin(pending: PendingLogin): PendingLogin {
        val result = json("/v1/auth/gravitas/start", "POST", JSONObject().put("state", pending.state).put("codeChallenge", GravitasLogin.challenge(pending.verifier)))
        val address = GravitasLogin.authorization(pending, result.getString("authorizationUrl"), result.getString("redirectUri"))
        return pending.copy(authorizationUrl = address)
    }
    fun finishLogin(pending: PendingLogin, code: String): Session {
        val result = json("/v1/auth/gravitas/exchange", "POST", JSONObject().put("code", code).put("codeVerifier", pending.verifier).put("deviceName", pending.deviceName))
        return Session(pending.baseUrl, result.getString("token"), result.getString("account"))
    }
    fun revoke() { json("/v1/device", "DELETE") }
    fun apps(): List<StoreApp> {
        val array = json("/v1/apps").getJSONArray("apps")
        return (0 until array.length()).map { i ->
            val obj = array.getJSONObject(i); val r = obj.optJSONObject("release")
            StoreApp(obj.getString("id"), obj.getString("name"), obj.getString("packageName"), obj.getString("certificateSha256"), obj.getString("description"), r?.let {
                StoreRelease(it.getLong("id"), it.getLong("versionCode"), it.getString("versionName"), it.getString("sha256"), it.getLong("size"), it.getInt("minSdk"), it.getString("notes"))
            })
        }
    }
    fun download(context: Context, app: StoreApp, progress: (Long, Long) -> Unit = { _, _ -> }): File {
        val release = requireNotNull(app.release)
        require(Versions.validDigest(release.sha256) && release.size in 1..314_572_800L) { "Invalid release metadata" }
        val dir = File(context.cacheDir, "apks").apply { mkdirs() }
        val dest = File(dir, "${release.sha256}.apk")
        val temp = File.createTempFile("download-", ".part", dir)
        val conn = connection("/v1/releases/${release.id}/apk")
        try {
            if (conn.responseCode != 200) throw ApiException(conn.responseCode, "APK download failed (${conn.responseCode})")
            val hash = MessageDigest.getInstance("SHA-256"); var size = 0L; var lastProgress = 0L
            conn.inputStream.use { input -> temp.outputStream().use { output ->
                val buf = ByteArray(64 * 1024)
                while (true) { val n = input.read(buf); if (n < 0) break; size += n; require(size <= release.size) { "APK exceeds expected size" }; hash.update(buf, 0, n); output.write(buf, 0, n); if (size - lastProgress >= 262144 || size == release.size) { progress(size, release.size); lastProgress = size } }
            } }
            require(size == release.size && hash.digest().toHex() == release.sha256) { "APK checksum or size mismatch" }
            require(temp.renameTo(dest)) { "Cannot save APK" }
            // Cache is only for installation handoff, never an archive of old APKs.
            dir.listFiles()?.filter { it != dest && it.lastModified() < System.currentTimeMillis() - 86_400_000 }?.forEach { it.delete() }
            return dest
        } finally { conn.disconnect(); temp.delete() }
    }
}
fun ByteArray.toHex() = joinToString("") { "%02x".format(it) }
