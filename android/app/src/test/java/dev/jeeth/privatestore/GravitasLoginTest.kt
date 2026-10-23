package dev.jeeth.privatestore

import java.net.URLEncoder
import org.junit.Assert.*
import org.junit.Test

class GravitasLoginTest {
    private val now = 1_000_000L
    private val pending = GravitasLogin.create(GravitasLogin.STORE, "My phone", now)
    private fun callback(query: String) = pending.baseUrl + GravitasLogin.CALLBACK + "?" + query
    private fun rejected(value: String, at: Long = now) = assertTrue(runCatching { GravitasLogin.code(pending, value, at) }.isFailure)
    @Test fun usesIndependentRandomProofsAndS256() {
        assertEquals(43,pending.state.length);assertEquals(43,pending.verifier.length)
        assertNotEquals(pending.state,pending.verifier)
        assertNotEquals(pending.state,GravitasLogin.create(pending.baseUrl,"Other",now).state)
        assertEquals("E9Melhoa2OwvFrEMTJguCHaoeK1t8URWbuGJSstw-cM",GravitasLogin.challenge("dBjftJeZ4CVP-mB92K27uhbUJU1p1r_wW1gFWFOEjXk"))
    }
    @Test fun callbackRequiresOriginStateAndFreshProof() {
        assertEquals("one-use-code",GravitasLogin.code(pending,callback("code=one-use-code&state=${pending.state}"),now))
        for (query in listOf("code=bad&state=other","code=a&code=b&state=${pending.state}","code=a&state=${pending.state}&state=${pending.state}","state=${pending.state}","code=a&state=${pending.state}&error=access_denied")) rejected(callback(query))
        val valid=callback("code=one&state=${pending.state}")
        rejected(valid.replace("https:","http:"));rejected(valid.replace("apps.gravitas.cool","evil.example"));rejected(valid.replace("/auth/android/callback","/auth/gravitas/callback"));rejected(valid+"#other")
        rejected(valid,pending.expiresAt);rejected(valid,now-20_000)
    }
    @Test fun authorizationCannotChangeProviderCallbackStateOrPermissions() {
        val params=mapOf("callerURL" to pending.baseUrl,"redirectURL" to pending.baseUrl+GravitasLogin.CALLBACK,"codeChallenge" to GravitasLogin.challenge(pending.verifier),"codeChallengeMethod" to "S256","scope" to "profile.name native.broker","state" to pending.state)
        val address="https://gravitas.cool/oauth/start?"+params.entries.joinToString("&") { (k,v)->k+"="+URLEncoder.encode(v,"UTF-8") }
        assertEquals(address,GravitasLogin.authorization(pending,address,pending.baseUrl+GravitasLogin.CALLBACK))
        for (bad in listOf(address.replace("gravitas.cool/oauth","evil.example/oauth"),address+"&state=other",address.replace("profile.name","chats.read"),address.replace("S256","plain"),address+"#x")) {
            assertTrue(runCatching { GravitasLogin.authorization(pending,bad,pending.baseUrl+GravitasLogin.CALLBACK) }.isFailure)
        }
    }
}
