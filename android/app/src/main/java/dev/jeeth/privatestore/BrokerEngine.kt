package dev.jeeth.privatestore

import android.content.Context
import org.json.JSONObject
import java.util.concurrent.locks.ReentrantLock
import kotlin.concurrent.withLock

class BrokerEngine(private val context: Context,private val iam: BrokerIam=BrokerIam()) {
    private val lock=ReentrantLock()
    private fun current(): BrokerSession {
        val session=runCatching { BrokerVault(context).load() }.getOrNull()
        if(session==null || session.sessionExpiresAt<=System.currentTimeMillis()/1000) { BrokerManager.clear(context,true);throw BrokerFailure(BrokerError.LOGIN_REQUIRED) }
        return session
    }
    private fun rotate(session: BrokerSession): BrokerSession {
        try {
            val fresh=iam.refresh(session)
            // Persist the rotated pair atomically before any further network call:
            // the old refresh has already been consumed even if status later fails.
            if(!BrokerVault(context).replace(session,fresh)) throw BrokerFailure(BrokerError.CANCELLED)
            iam.status(fresh)
            return fresh
        } catch(e:BrokerFailure) { if(e.reason==BrokerError.LOGIN_REQUIRED) BrokerManager.clear(context,true);throw e }
    }
    fun code(account: String,request: BrokerRequest): JSONObject = lock.withLock {
        var session=current()
        if(session.account!=account) throw BrokerFailure(BrokerError.LOGIN_REQUIRED)
        var refreshed=false
        if(session.accessExpiresAt<=System.currentTimeMillis()/1000+60) { session=rotate(session);refreshed=true }
        try { iam.authorize(session,request).also { if(BrokerVault(context).load()?.sessionId!=session.sessionId) throw BrokerFailure(BrokerError.CANCELLED) } } catch(e:BrokerFailure) {
            if(e.reason!=BrokerError.LOGIN_REQUIRED) throw e
            if(refreshed) { BrokerManager.clear(context,true);throw e }
            session=rotate(session)
            try { iam.authorize(session,request).also { if(BrokerVault(context).load()?.sessionId!=session.sessionId) throw BrokerFailure(BrokerError.CANCELLED) } } catch(retry:BrokerFailure) { if(retry.reason==BrokerError.LOGIN_REQUIRED) BrokerManager.clear(context,true);throw retry }
        }
    }
    fun refreshForUpdates() = lock.withLock {
        val session=runCatching { BrokerVault(context).load() }.getOrNull()?:return@withLock
        if(session.sessionExpiresAt<=System.currentTimeMillis()/1000) { BrokerManager.clear(context,true);throw BrokerFailure(BrokerError.LOGIN_REQUIRED) }
        try { iam.status(session);if(session.accessExpiresAt<=System.currentTimeMillis()/1000+300) rotate(session) }
        catch(e:BrokerFailure) { if(e.reason==BrokerError.LOGIN_REQUIRED) BrokerManager.clear(context,true);throw e }
    }
    fun accept(session: BrokerSession,device: Session?=null) = lock.withLock {
        val now=System.currentTimeMillis()/1000
        if(session.sessionExpiresAt<=now || session.sessionExpiresAt>now+2_592_060 || session.accessExpiresAt>now+3660) throw BrokerFailure(BrokerError.UNAVAILABLE)
        if(device!=null) SessionVault(context).save(device)
        BrokerVault(context).save(session);BrokerAccounts.reconcile(context)
    }
}
object BrokerManager {
    @Volatile private var instance: BrokerEngine?=null
    fun engine(context: Context): BrokerEngine = instance?:synchronized(this) { instance?:BrokerEngine(context.applicationContext).also { instance=it } }
    fun clear(context: Context,device: Boolean) {
        BrokerVault(context).clear();BrokerAccounts.clear(context)
        if(device) { SessionVault(context).clear();UpdateWorker.cancel(context) }
    }
}
