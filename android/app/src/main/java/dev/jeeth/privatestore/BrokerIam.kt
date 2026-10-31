package dev.jeeth.privatestore

import org.json.JSONObject
import java.net.HttpURLConnection
import java.net.URL
import java.net.URLEncoder

data class BrokerReply(val status: Int,val body: String) { override fun toString() = "BrokerReply(status=$status, body=redacted)" }
fun interface BrokerTransport { fun request(url: String,bearer: String?,body: String?,form: Boolean): BrokerReply }
class BrokerHTTP : BrokerTransport {
    override fun request(url: String,bearer: String?,body: String?,form: Boolean): BrokerReply {
        if (!url.startsWith(BrokerProtocol.ISSUER+"/")) throw BrokerFailure(BrokerError.UNAVAILABLE)
        val conn=URL(url).openConnection() as HttpURLConnection
        try {
            conn.instanceFollowRedirects=false;conn.connectTimeout=10_000;conn.readTimeout=12_000
            conn.setRequestProperty("Accept","application/json")
            if (bearer!=null) conn.setRequestProperty("Authorization","Bearer $bearer")
            if (body!=null) { conn.requestMethod="POST";conn.doOutput=true;conn.setRequestProperty("Content-Type",if(form) "application/x-www-form-urlencoded" else "application/json");conn.outputStream.use { it.write(body.toByteArray(Charsets.UTF_8)) } }
            val status=conn.responseCode;val stream=if(status in 200..299) conn.inputStream else conn.errorStream
            val text=stream?.use { input -> val output=java.io.ByteArrayOutputStream();val buf=ByteArray(4096);while(true) { val n=input.read(buf);if(n<0) break;if(output.size()+n>65536) throw BrokerFailure(BrokerError.UNAVAILABLE);output.write(buf,0,n) };String(output.toByteArray(),Charsets.UTF_8) }.orEmpty()
            return BrokerReply(status,text)
        } catch (e:BrokerFailure) { throw e } catch (_:Exception) { throw BrokerFailure(BrokerError.UNAVAILABLE) } finally { conn.disconnect() }
    }
}
class BrokerIam(private val transport: BrokerTransport=BrokerHTTP()) {
    private fun json(reply: BrokerReply): JSONObject = try { JSONObject(reply.body) } catch (_:Exception) { throw BrokerFailure(BrokerError.UNAVAILABLE) }
    private fun discovery() {
        val r=transport.request(BrokerProtocol.ISSUER+"/.well-known/iam-configuration",null,null,false)
        if(r.status!=200) throw BrokerFailure(BrokerError.UNAVAILABLE)
        val d=json(r)
        if(d.optString("issuer")!=BrokerProtocol.ISSUER || d.optString("identity_domain")!="gravitas.cool" || d.optString("token_endpoint")!=BrokerProtocol.ISSUER+"/oauth/token" || d.optString("session_check_endpoint")!=BrokerProtocol.ISSUER+"/oauth/session" || d.optString("native_broker_authorization_endpoint")!=BrokerProtocol.ISSUER+"/oauth/broker/authorize") throw BrokerFailure(BrokerError.UNAVAILABLE)
    }
    fun status(session: BrokerSession) {
        discovery()
        val r=transport.request(BrokerProtocol.ISSUER+"/oauth/session",session.sessionCheckToken,null,false)
        if(r.status==401) throw BrokerFailure(BrokerError.LOGIN_REQUIRED)
        if(r.status!=200) throw BrokerFailure(BrokerError.UNAVAILABLE)
        val d=json(r)
        if(!d.optBoolean("active") || d.optString("iss")!=session.issuer || d.optString("sub")!=session.subject || d.optString("app_origin")!=session.appOrigin || d.optString("session_id")!=session.sessionId || d.optLong("exp")!=session.sessionExpiresAt || d.optLong("exp")<=System.currentTimeMillis()/1000) throw BrokerFailure(BrokerError.UNAVAILABLE)
    }
    fun refresh(session: BrokerSession): BrokerSession {
        discovery()
        val form="grant_type=refresh_token&refresh_token="+URLEncoder.encode(session.refreshToken,"UTF-8")
        val r=transport.request(BrokerProtocol.ISSUER+"/oauth/token",null,form,true)
        if(r.status==400 || r.status==401) throw BrokerFailure(BrokerError.LOGIN_REQUIRED)
        if(r.status!=200) throw BrokerFailure(BrokerError.UNAVAILABLE)
        val d=json(r);val now=System.currentTimeMillis()/1000;val lifetime=d.optLong("expires_in");val familyRemaining=d.optLong("session_check_expires_in")
        if(d.optString("token_type").lowercase()!="bearer" || !BrokerProtocol.scopes(d.optString("scope")) || d.optString("session_id")!=session.sessionId || lifetime !in 1..3600 || familyRemaining !in 1..2_592_000 || kotlin.math.abs((session.sessionExpiresAt-now)-familyRemaining)>60) throw BrokerFailure(BrokerError.UNAVAILABLE)
        return BrokerSession.parse(session.copy(accessToken=d.optString("access_token"),refreshToken=d.optString("refresh_token"),sessionCheckToken=d.optString("session_check_token"),scope=d.optString("scope"),accessExpiresAt=minOf(now+lifetime,session.sessionExpiresAt)).json())
    }
    fun authorize(session: BrokerSession,request: BrokerRequest): JSONObject {
        discovery()
        val target=request.client
        val body=JSONObject().put("app_origin",target.origin).put("redirect_uri",target.redirect).put("scope",target.scope).put("code_challenge_method","S256").put("code_challenge",request.challenge).put("state",request.state)
        val r=transport.request(BrokerProtocol.ISSUER+"/oauth/broker/authorize",session.accessToken,body.toString(),false)
        if(r.status==401) throw BrokerFailure(BrokerError.LOGIN_REQUIRED)
        if(r.status==403) throw BrokerFailure(if(json(r).optString("error")=="consent_required") BrokerError.CONSENT_REQUIRED else BrokerError.UNAUTHORIZED_CLIENT)
        if(r.status==400) throw BrokerFailure(BrokerError.INVALID_REQUEST)
        if(r.status!=200) throw BrokerFailure(BrokerError.UNAVAILABLE)
        val d=json(r);val code=d.optString("code")
        if(d.optString("issuer")!=session.issuer || d.optString("app_origin")!=target.origin || d.optString("redirect_uri")!=target.redirect || d.optString("state")!=request.state || d.optString("session_id")!=session.sessionId || d.optString("sub")!=session.subject || d.optLong("expires_in") !in 1..60 || code.isBlank() || code.length>2048 || code.any { it.isWhitespace() }) throw BrokerFailure(BrokerError.UNAVAILABLE)
        return d
    }
}
