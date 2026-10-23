package dev.jeeth.privatestore

import android.accounts.Account
import android.accounts.AccountManager
import android.content.Context
import android.content.pm.PackageManager
import android.os.Build
import java.security.MessageDigest

object BrokerAccounts {
    private val lock=Any()
    fun type(context: Context) = context.packageName+".gravitas"
    @Suppress("DEPRECATION")
    fun verifiedCaller(context: Context,uid: Int,declared: String?): Boolean {
        if(uid<0) return false
        val pm=context.packageManager
        val packages=pm.getPackagesForUid(uid)?.toList()?:return false
        val signatures=try {
            if(Build.VERSION.SDK_INT>=28) pm.getPackageInfo(BrokerProtocol.CLIENT_PACKAGE,PackageManager.GET_SIGNING_CERTIFICATES).signingInfo?.apkContentsSigners?.toList().orEmpty()
            else pm.getPackageInfo(BrokerProtocol.CLIENT_PACKAGE,PackageManager.GET_SIGNATURES).signatures?.toList().orEmpty()
        } catch (_:PackageManager.NameNotFoundException) { return false }
        return BrokerProtocol.approved(packages,declared,signatures.map { MessageDigest.getInstance("SHA-256").digest(it.toByteArray()).toHex() })
    }
    fun reconcile(context: Context) = synchronized(lock) {
        val manager=AccountManager.get(context)
        val saved=runCatching { BrokerVault(context).load() }.getOrNull()
        val accounts=manager.getAccountsByType(type(context))
        accounts.filter { saved==null || it.name!=saved.account }.forEach { manager.removeAccountExplicitly(it) }
        if(saved!=null && saved.sessionExpiresAt>System.currentTimeMillis()/1000) {
            val account=Account(saved.account,type(context))
            val uid=runCatching { context.packageManager.getApplicationInfo(BrokerProtocol.CLIENT_PACKAGE,0).uid }.getOrDefault(-1)
            val visible=verifiedCaller(context,uid,BrokerProtocol.CLIENT_PACKAGE)
            val visibility=mapOf(context.packageName to AccountManager.VISIBILITY_VISIBLE,BrokerProtocol.CLIENT_PACKAGE to if(visible) AccountManager.VISIBILITY_VISIBLE else AccountManager.VISIBILITY_NOT_VISIBLE,AccountManager.PACKAGE_NAME_KEY_LEGACY_VISIBLE to AccountManager.VISIBILITY_USER_MANAGED_NOT_VISIBLE,AccountManager.PACKAGE_NAME_KEY_LEGACY_NOT_VISIBLE to AccountManager.VISIBILITY_USER_MANAGED_NOT_VISIBLE)
            manager.addAccountExplicitly(account,null,null,visibility)
            visibility.forEach { (pkg,value)->manager.setAccountVisibility(account,pkg,value) }
        } else accounts.forEach { manager.removeAccountExplicitly(it) }
    }
    fun clear(context: Context) = synchronized(lock) { val manager=AccountManager.get(context);manager.getAccountsByType(type(context)).forEach { manager.removeAccountExplicitly(it) } }
}
