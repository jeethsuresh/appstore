package dev.jeeth.privatestore

import android.content.ContentProvider
import android.content.ContentValues
import android.database.Cursor
import android.net.Uri
import android.os.Binder
import android.os.Bundle

// A foreground client can request visibility reconciliation immediately after an
// external APK install. Binder supplies the caller UID; no claimed package or
// credential is accepted. All data operations are deliberately unsupported.
class BrokerVisibilityProvider : ContentProvider() {
    override fun onCreate() = true
    override fun call(method: String,arg: String?,extras: Bundle?): Bundle {
        val ctx=requireNotNull(context)
        val uid=Binder.getCallingUid()
        val caller=ctx.packageManager.getPackagesForUid(uid)?.singleOrNull()
        if(method!="prepare" || arg!=null || (extras!=null && !extras.isEmpty) || !BrokerAccounts.verifiedCaller(ctx,uid,caller)) throw SecurityException("Unapproved broker caller")
        BrokerAccounts.reconcile(ctx)
        val available=android.accounts.AccountManager.get(ctx).getAccountsByType(BrokerAccounts.type(ctx)).size==1
        return Bundle().apply { putInt("protocolVersion",1);putBoolean("available",available) }
    }
    override fun query(uri: Uri,projection: Array<out String>?,selection: String?,selectionArgs: Array<out String>?,sortOrder: String?): Cursor? = throw UnsupportedOperationException()
    override fun getType(uri: Uri): String? = throw UnsupportedOperationException()
    override fun insert(uri: Uri,values: ContentValues?): Uri? = throw UnsupportedOperationException()
    override fun delete(uri: Uri,selection: String?,selectionArgs: Array<out String>?): Int = throw UnsupportedOperationException()
    override fun update(uri: Uri,values: ContentValues?,selection: String?,selectionArgs: Array<out String>?): Int = throw UnsupportedOperationException()
}
