package dev.jeeth.privatestore

import android.content.Context
import androidx.work.Constraints
import androidx.work.CoroutineWorker
import androidx.work.ExistingPeriodicWorkPolicy
import androidx.work.NetworkType
import androidx.work.PeriodicWorkRequestBuilder
import androidx.work.WorkManager
import androidx.work.WorkerParameters
import kotlinx.coroutines.CancellationException
import kotlinx.coroutines.async
import kotlinx.coroutines.awaitAll
import kotlinx.coroutines.coroutineScope
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.withContext
import kotlinx.coroutines.sync.Mutex
import kotlinx.coroutines.sync.withLock
import java.util.concurrent.TimeUnit

class UpdateWorker(context: Context, params: WorkerParameters) : CoroutineWorker(context, params) {
    override suspend fun doWork(): Result = withContext(Dispatchers.IO) {
        updateLock.withLock {
            val context = applicationContext
            val prefs = context.getSharedPreferences("updates", Context.MODE_PRIVATE)
            var checkedSession: Session? = null
            try {
                BrokerAccounts.reconcile(context)
                BrokerManager.engine(context).refreshForUpdates()
                val session = SessionVault(context).load() ?: return@withContext Result.success()
                checkedSession = session
                val api = StoreApi(session)
                if (!prefs.getBoolean("appNotifications", true)) api.clearNotificationApps(NotificationRelay.registrationGeneration(context))
                val failed = java.util.concurrent.atomic.AtomicBoolean(false)
                // Commit other updates before replacing this process with the store update.
                val catalog = api.apps()
                suspend fun update(app: StoreApp) {
                    val release = app.release ?: return
                    val installed = Installer.installed(context, app.packageName) ?: return
                    if (!Versions.newer(installed, release.versionCode) || release.minSdk > android.os.Build.VERSION.SDK_INT) return
                    if (!prefs.getBoolean("auto", true) || !context.packageManager.canRequestPackageInstalls() || !Notices.enabled(context)) {
                        Notices.show(context, 100, "Updates available", "Open Private Store to update your apps.")
                        return
                    }
                    if (prefs.getLong("pending.${app.packageName}", 0) == release.versionCode && context.packageManager.packageInstaller.mySessions.any { it.appPackageName == app.packageName }) return
                    try { Downloads.install(context, session, app, background = true) }
                    catch (e: CancellationException) { throw e }
                    catch (e: Exception) { failed.set(true); prefs.edit().putString("status", "${app.name}: ${e.message}").apply() }
                }
                coroutineScope { catalog.filter { it.packageName != context.packageName }.map { app -> async { update(app) } }.awaitAll() }
                catalog.firstOrNull { it.packageName == context.packageName }?.let { update(it) }
                prefs.edit().putLong("lastCheck", System.currentTimeMillis()).apply()
                if (failed.get()) Result.retry() else Result.success()
            } catch (e: CancellationException) { throw e }
            catch (e: ApiException) {
                prefs.edit().putString("status", e.message).apply()
                if (e.code == 401) {
                    if (checkedSession?.let { BrokerManager.engine(context).invalidateDevice(it) } == true) Notices.show(context, 101, "Private Store needs sign-in", e.message ?: "Sign in again")
                    Result.success()
                } else Result.retry()
            } catch(e:BrokerFailure) { prefs.edit().putString("status",if(e.reason==BrokerError.LOGIN_REQUIRED) "Sign in with Gravitas again" else "Gravitas is temporarily unavailable").apply();if(e.reason==BrokerError.LOGIN_REQUIRED) Result.success() else Result.retry()
            } catch (e: Exception) { prefs.edit().putString("status", "Update check failed: ${e.message}").apply(); Result.retry() }
        }
    }
    companion object {
        private val updateLock = Mutex()
        fun schedule(context: Context) {
            val request = PeriodicWorkRequestBuilder<UpdateWorker>(6, TimeUnit.HOURS).setConstraints(Constraints.Builder().setRequiredNetworkType(NetworkType.CONNECTED).setRequiresBatteryNotLow(true).build()).build()
            WorkManager.getInstance(context).enqueueUniquePeriodicWork("store-updates", ExistingPeriodicWorkPolicy.KEEP, request)
        }
        fun cancel(context: Context) { WorkManager.getInstance(context).cancelUniqueWork("store-updates") }
    }
}
