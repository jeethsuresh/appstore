package dev.jeeth.privatestore

import android.content.BroadcastReceiver
import android.content.Context
import android.content.Intent

/** Android starts the new package after terminating the installer process. */
class StoreReplacedReceiver : BroadcastReceiver() {
    override fun onReceive(context: Context, intent: Intent) {
        if (intent.action != Intent.ACTION_MY_PACKAGE_REPLACED) return
        SelfUpdateRecovery.reconcile(context, replaced = true)
    }
}

object SelfUpdateRecovery {
    fun reconcile(context: Context, replaced: Boolean = false) {
        val prefs = context.getSharedPreferences("updates", Context.MODE_PRIVATE)
        val pending = prefs.getLong("pending.${context.packageName}", 0)
        val installed = Installer.installed(context, context.packageName) ?: 0
        if (replaced || (pending > 0 && installed >= pending)) {
            check(prefs.edit().remove("pending.${context.packageName}")
                .putString("status", "Private Store updated successfully").commit())
        }
        // Package replacement retains the app UID, private data and Keystore keys.
        // Re-establish the unique schedule even if the old worker died at commit.
        if (runCatching { SessionVault(context).load() }.getOrNull() != null) UpdateWorker.schedule(context)
        runCatching { BrokerAccounts.reconcile(context) }
    }
}
