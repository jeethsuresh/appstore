package dev.jeeth.privatestore

import android.accounts.Account
import android.accounts.AccountAuthenticatorResponse
import android.accounts.AccountManager
import android.os.Bundle
import androidx.test.platform.app.InstrumentationRegistry
import org.json.JSONObject
import org.junit.Assert.*
import org.junit.Assume.assumeTrue
import org.junit.Before
import org.junit.After
import org.junit.Test
import java.util.concurrent.CountDownLatch
import java.util.concurrent.Executors
import java.util.concurrent.TimeUnit

class BrokerDeviceTest {
    private val context=InstrumentationRegistry.getInstrumentation().targetContext
    private val now get()=System.currentTimeMillis()/1000
    private fun sample(expiredAccess: Boolean=false)=BrokerSession(BrokerProtocol.ISSUER,GravitasLogin.STORE,"fixture-subject","@fixture:gravitas.cool","a".repeat(43),"b".repeat(43),"profile.name native.broker",now+if(expiredAccess) -1 else 1200,"fixture-family","c".repeat(43),now+7200)
    @Before fun isolated() { assumeTrue(context.packageName.endsWith(".testbed"));BrokerManager.clear(context,true) }
    @After fun cleanup() { if(context.packageName.endsWith(".testbed")) BrokerManager.clear(context,true) }
    private fun failure(reason: BrokerError,block:()->Unit) { try { block();fail("Expected ${reason.wire}") } catch(e:BrokerFailure) { assertEquals(reason,e.reason) } }
    private class Fake(val initial: BrokerSession):BrokerTransport {
        var status=200;var malformed=false;var refreshes=0;var codes=0
        var refreshEntered:CountDownLatch?=null;var releaseRefresh:CountDownLatch?=null
        override fun request(url:String,bearer:String?,body:String?,form:Boolean):BrokerReply {
            val s=initial
            val value=when {
                url.endsWith("iam-configuration")->JSONObject().put("issuer",s.issuer).put("identity_domain","gravitas.cool").put("token_endpoint",s.issuer+"/oauth/token").put("session_check_endpoint",s.issuer+"/oauth/session").put("native_broker_authorization_endpoint",s.issuer+"/oauth/broker/authorize")
                url.endsWith("/session")-> { if(status!=200) return BrokerReply(status,"{}");JSONObject().put("active",true).put("iss",s.issuer).put("sub",if(malformed) "wrong" else s.subject).put("app_origin",s.appOrigin).put("session_id",s.sessionId).put("exp",s.sessionExpiresAt) }
                url.endsWith("/token")-> { refreshes++;refreshEntered?.countDown();releaseRefresh?.await(10,TimeUnit.SECONDS);JSONObject().put("token_type","Bearer").put("scope",s.scope).put("session_id",s.sessionId).put("expires_in",1000).put("session_check_expires_in",s.sessionExpiresAt-System.currentTimeMillis()/1000).put("access_token","d".repeat(43)).put("refresh_token","e".repeat(43)).put("session_check_token","f".repeat(43)) }
                else->{ codes++;val request=JSONObject(body!!);JSONObject().put("issuer",s.issuer).put("app_origin",BrokerProtocol.ORIGIN).put("redirect_uri",BrokerProtocol.REDIRECT).put("state",request.getString("state")).put("session_id",s.sessionId).put("sub",s.subject).put("expires_in",60).put("code","single-use-$codes") }
            }
            return BrokerReply(200,value.toString())
        }
    }
    @Test fun vaultEncryptsAndRejectsStaleRotationAfterLogout() {
        val s=sample();val vault=BrokerVault(context);vault.save(s)
        assertEquals(s,vault.load());assertFalse(s.toString().contains(s.refreshToken))
        val stored=context.getSharedPreferences("gravitas-broker",0).all.toString()
        assertFalse(stored.contains(s.refreshToken));assertFalse(stored.contains(s.sessionCheckToken))
        vault.clear();assertFalse(vault.replace(s,s.copy(refreshToken="z".repeat(43))));assertNull(vault.load())
    }
    @Test fun concurrentCodesRotateOnceAndKeepNonceBinding() {
        val s=sample(true);val fake=Fake(s);BrokerVault(context).save(s);val engine=BrokerEngine(context,BrokerIam(fake));val pool=Executors.newFixedThreadPool(2)
        try {
            val results=(1..2).map { n->pool.submit<String> { engine.code(s.account,BrokerRequest("g".repeat(43),n.toString().repeat(43))).getString("state") } }.map { it.get(20,TimeUnit.SECONDS) }
            assertEquals(listOf("1".repeat(43),"2".repeat(43)),results);assertEquals(1,fake.refreshes);assertEquals(2,fake.codes)
            assertEquals(s.sessionExpiresAt,BrokerVault(context).load()!!.sessionExpiresAt)
        } finally { pool.shutdownNow() }
    }
    @Test fun outageAfterRotationKeepsNewPairAndExplicitRevocationClearsBothVaults() {
        val s=sample(true);val fake=Fake(s);val engine=BrokerEngine(context,BrokerIam(fake));BrokerVault(context).save(s);SessionVault(context).save(Session(GravitasLogin.STORE,"fixture-device"))
        fake.status=503
        failure(BrokerError.UNAVAILABLE) { engine.code(s.account,BrokerRequest("g".repeat(43),"h".repeat(43))) }
        assertEquals("e".repeat(43),BrokerVault(context).load()!!.refreshToken);assertNotNull(SessionVault(context).load())
        fake.status=200;fake.malformed=true
        failure(BrokerError.UNAVAILABLE) { engine.refreshForUpdates() };assertNotNull(BrokerVault(context).load())
        fake.status=401
        failure(BrokerError.LOGIN_REQUIRED) { engine.refreshForUpdates() };assertNull(BrokerVault(context).load());assertNull(SessionVault(context).load())
    }
    @Test fun localLogoutDuringRefreshCannotRestoreCredentials() {
        val s=sample(true);val fake=Fake(s);fake.refreshEntered=CountDownLatch(1);fake.releaseRefresh=CountDownLatch(1)
        BrokerVault(context).save(s);val engine=BrokerEngine(context,BrokerIam(fake));val pool=Executors.newSingleThreadExecutor()
        try {
            val task=pool.submit { failure(BrokerError.CANCELLED) { engine.code(s.account,BrokerRequest("g".repeat(43),"h".repeat(43))) } }
            assertTrue(fake.refreshEntered!!.await(10,TimeUnit.SECONDS));BrokerManager.clear(context,true);fake.releaseRefresh!!.countDown();task.get(20,TimeUnit.SECONDS);assertNull(BrokerVault(context).load())
        } finally { fake.releaseRefresh!!.countDown();pool.shutdownNow() }
    }
    @Test fun authenticatorRejectsCallerBeforeNetworkAndReturnsOnlyBoundCode() {
        val s=sample();val fake=Fake(s);BrokerVault(context).save(s);val engine=BrokerEngine(context,BrokerIam(fake))
        val options=Bundle().apply { putInt("protocolVersion",1);putString("codeChallenge","g".repeat(43));putString("state","h".repeat(43));putInt(AccountManager.KEY_CALLER_UID,123);putString(AccountManager.KEY_ANDROID_PACKAGE_NAME,BrokerProtocol.CLIENT_PACKAGE) }
        val account=Account(s.account,BrokerAccounts.type(context))
        // A local response object is unused by the synchronous implementation.
        val parcel=android.os.Parcel.obtain();parcel.writeStrongBinder(null);parcel.setDataPosition(0)
        val response=AccountAuthenticatorResponse.CREATOR.createFromParcel(parcel);parcel.recycle()
        val denied=GravitasAuthenticator(context,engine) { _,_->false }.getAuthToken(response,account,"invalid",options)
        assertEquals("unauthorized_client",denied.getString("brokerError"));assertEquals(0,fake.codes)
        val auth=GravitasAuthenticator(context,engine) { _,_->true }
        val type=BrokerProtocol.PREFIX+"g".repeat(43)+":"+"h".repeat(43)
        val result=auth.getAuthToken(response,account,type,options)
        assertEquals("single-use-1",result.getString(AccountManager.KEY_AUTHTOKEN));assertEquals(0,result.getLong("android.accounts.expiry",0));assertEquals("h".repeat(43),result.getString("state"))
        assertEquals(setOf(AccountManager.KEY_ACCOUNT_NAME,AccountManager.KEY_ACCOUNT_TYPE,AccountManager.KEY_AUTHTOKEN,"android.accounts.expiry","protocolVersion","state","codeChallenge","issuer","appOrigin","redirectUri","sessionId","subject"),result.keySet())
        options.putString("state","i".repeat(43));assertEquals("invalid_request",auth.getAuthToken(response,account,type,options).getString("brokerError"));assertEquals(1,fake.codes)
    }
    @Test fun visibilityProviderRejectsUnapprovedUidAndAllDataOperations() {
        val uri=android.net.Uri.parse("content://${context.packageName}.gravitas")
        for(method in listOf("prepare","unknown")) {
            try { context.contentResolver.call(uri,method,null,null);fail("Unapproved caller accepted") } catch(_:SecurityException) { }
        }
        try { context.contentResolver.query(uri,null,null,null,null);fail("Provider exposed account data") } catch(_:UnsupportedOperationException) { }
        assertFalse(BrokerAccounts.verifiedCaller(context,android.os.Process.myUid(),BrokerProtocol.CLIENT_PACKAGE))
    }
}
