package dev.jeeth.privatestore

import android.content.Context
import android.security.keystore.KeyGenParameterSpec
import android.security.keystore.KeyProperties
import android.util.Base64
import org.json.JSONObject
import java.security.KeyStore
import javax.crypto.Cipher
import javax.crypto.KeyGenerator
import javax.crypto.SecretKey
import javax.crypto.spec.GCMParameterSpec

data class BrokerSession(val issuer: String,val appOrigin: String,val subject: String,val account: String,val accessToken: String,val refreshToken: String,val scope: String,val accessExpiresAt: Long,val sessionId: String,val sessionCheckToken: String,val sessionExpiresAt: Long) {
    override fun toString() = "BrokerSession(credentials=redacted)"
    fun json() = JSONObject().put("issuer",issuer).put("appOrigin",appOrigin).put("subject",subject).put("account",account).put("accessToken",accessToken).put("refreshToken",refreshToken).put("scope",scope).put("accessExpiresAt",accessExpiresAt).put("sessionId",sessionId).put("sessionCheckToken",sessionCheckToken).put("sessionExpiresAt",sessionExpiresAt)
    companion object {
        fun parse(value: JSONObject): BrokerSession {
            try {
                val result=BrokerSession(value.getString("issuer"),value.getString("appOrigin"),value.getString("subject"),value.getString("account"),value.getString("accessToken"),value.getString("refreshToken"),value.getString("scope"),value.getLong("accessExpiresAt"),value.getString("sessionId"),value.getString("sessionCheckToken"),value.getLong("sessionExpiresAt"))
                require(result.issuer==BrokerProtocol.ISSUER && result.appOrigin==GravitasLogin.STORE && result.subject.isNotBlank() && result.subject.length<=256 && result.account.isNotBlank() && result.account.length<=256 && result.sessionId.length in 1..128 && BrokerProtocol.scopes(result.scope))
                require(listOf(result.accessToken,result.refreshToken,result.sessionCheckToken).all(BrokerProtocol::credential))
                require(result.accessExpiresAt>0 && result.accessExpiresAt<=result.sessionExpiresAt && result.sessionExpiresAt>0)
                return result
            } catch (_:Exception) { throw BrokerFailure(BrokerError.UNAVAILABLE) }
        }
    }
}
class BrokerVault(context: Context) {
    companion object { private val lock=Any() }
    private val prefs=context.getSharedPreferences("gravitas-broker",Context.MODE_PRIVATE)
    private fun key(): SecretKey {
        val store=KeyStore.getInstance("AndroidKeyStore").apply { load(null) }
        (store.getKey("private-store-broker",null) as? SecretKey)?.let { return it }
        return KeyGenerator.getInstance(KeyProperties.KEY_ALGORITHM_AES,"AndroidKeyStore").apply { init(KeyGenParameterSpec.Builder("private-store-broker",KeyProperties.PURPOSE_ENCRYPT or KeyProperties.PURPOSE_DECRYPT).setBlockModes(KeyProperties.BLOCK_MODE_GCM).setEncryptionPaddings(KeyProperties.ENCRYPTION_PADDING_NONE).build()) }.generateKey()
    }
    fun save(value: BrokerSession) = synchronized(lock) {
        val cipher=Cipher.getInstance("AES/GCM/NoPadding").apply { init(Cipher.ENCRYPT_MODE,key());updateAAD("gravitas-broker-v1".toByteArray()) }
        val data=cipher.doFinal(value.json().toString().toByteArray(Charsets.UTF_8))
        check(prefs.edit().putString("iv",Base64.encodeToString(cipher.iv,Base64.NO_WRAP)).putString("data",Base64.encodeToString(data,Base64.NO_WRAP)).commit())
    }
    fun load(): BrokerSession? = synchronized(lock) {
        val data=prefs.getString("data",null)?:return@synchronized null
        val cipher=Cipher.getInstance("AES/GCM/NoPadding").apply { init(Cipher.DECRYPT_MODE,key(),GCMParameterSpec(128,Base64.decode(prefs.getString("iv",""),Base64.NO_WRAP)));updateAAD("gravitas-broker-v1".toByteArray()) }
        BrokerSession.parse(JSONObject(String(cipher.doFinal(Base64.decode(data,Base64.NO_WRAP)),Charsets.UTF_8)))
    }
    fun replace(previous: BrokerSession,fresh: BrokerSession): Boolean = synchronized(lock) {
        val current=load()
        if(current?.sessionId!=previous.sessionId || current.refreshToken!=previous.refreshToken) false
        else { save(fresh);true }
    }
    fun clear() = synchronized(lock) { check(prefs.edit().clear().commit()) }
}
