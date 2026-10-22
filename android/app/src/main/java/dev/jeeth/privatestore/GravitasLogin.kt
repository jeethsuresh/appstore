package dev.jeeth.privatestore

import java.net.URI
import java.net.URLDecoder
import java.security.MessageDigest
import java.security.SecureRandom
import java.util.Base64

data class PendingLogin(val baseUrl: String, val state: String, val verifier: String, val deviceName: String, val expiresAt: Long, val authorizationUrl: String = "")
object GravitasLogin {
    const val STORE = "https://apps.gravitas.cool"
    const val CALLBACK = "/auth/android/callback"
    private fun encode(bytes: ByteArray) = Base64.getUrlEncoder().withoutPadding().encodeToString(bytes)
    private fun random() = encode(ByteArray(32).also { SecureRandom().nextBytes(it) })
    fun create(base: String, deviceName: String, now: Long = System.currentTimeMillis()) = PendingLogin(Versions.baseUrl(base), random(), random(), deviceName.trim(), now + 600_000)
    fun challenge(verifier: String) = encode(MessageDigest.getInstance("SHA-256").digest(verifier.toByteArray(Charsets.US_ASCII)))
    private fun query(uri: URI): Map<String, List<String>> = uri.rawQuery.orEmpty().split('&').filter { it.isNotEmpty() }.map {
        val pair = it.split('=', limit = 2)
        URLDecoder.decode(pair[0], "UTF-8") to URLDecoder.decode(pair.getOrElse(1) { "" }, "UTF-8")
    }.groupBy({ it.first }, { it.second })
    fun authorization(pending: PendingLogin, address: String, redirect: String): String {
        val uri = URI(address); val q = query(uri)
        require(uri.scheme == "https" && uri.host == "gravitas.cool" && uri.port in listOf(-1, 443) && uri.userInfo == null && uri.fragment == null && uri.path == "/oauth/start") { "The store returned an untrusted sign-in address" }
        val expected = mapOf("callerURL" to pending.baseUrl, "redirectURL" to pending.baseUrl + CALLBACK, "codeChallenge" to challenge(pending.verifier), "codeChallengeMethod" to "S256", "scope" to "profile.name", "state" to pending.state)
        require(redirect == pending.baseUrl + CALLBACK && expected.all { (key, value) -> q[key] == listOf(value) }) { "The sign-in request does not match this device" }
        return uri.toASCIIString()
    }
    fun code(pending: PendingLogin, callback: String, now: Long = System.currentTimeMillis()): String {
        require(now < pending.expiresAt && pending.expiresAt <= now + 601_000) { "Sign-in expired. Start again." }
        val uri = URI(callback); val origin = URI(pending.baseUrl); val q = query(uri)
        require(uri.scheme == "https" && uri.host == origin.host && uri.port == origin.port && uri.userInfo == null && uri.fragment == null && uri.path == CALLBACK) { "Unexpected sign-in return address" }
        require(q["state"]?.size == 1 && MessageDigest.isEqual(q["state"]!![0].toByteArray(), pending.state.toByteArray())) { "This sign-in belongs to another request. Start again." }
        require(q["error"].isNullOrEmpty()) { "Sign-in was cancelled. You can try again." }
        val values = q["code"]
        require(values?.size == 1 && values[0].isNotEmpty() && values[0].length <= 2048) { "The sign-in code is missing or invalid" }
        return values!![0]
    }
}
