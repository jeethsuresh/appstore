package dev.jeeth.privatestore

import android.content.Context
import kotlinx.coroutines.*
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.asStateFlow
import kotlinx.coroutines.flow.update

/** One process-wide queue shared by the screen and the periodic worker. */
object Downloads {
    data class Transfer(val label: String, val fraction: Float? = null, val active: Boolean = true)
    private val scope = CoroutineScope(SupervisorJob() + Dispatchers.IO)
    private val queue = DownloadQueue(3)
    private val mutable = MutableStateFlow<Map<String, Transfer>>(emptyMap())
    val transfers = mutable.asStateFlow()

    fun enqueue(context: Context, session: Session, apps: List<StoreApp>) {
        val appContext = context.applicationContext
        scope.launch {
            // All other downloads are handed to Android before a self-update can replace us.
            coroutineScope { apps.filter { it.packageName != appContext.packageName }.map { app -> async { runCatching { install(appContext, session, app, false) } } }.awaitAll() }
            apps.firstOrNull { it.packageName == appContext.packageName }?.let { runCatching { install(appContext, session, it, false) } }
        }
    }
    suspend fun install(context: Context, session: Session, app: StoreApp, background: Boolean) {
        fun state(value: Transfer) { mutable.update { it + (app.packageName to value) } }
        try {
            queue.run(app.packageName, queued = { state(Transfer("Queued")) }) {
                withContext(Dispatchers.IO) {
                    check(SessionVault(context).load() == session) { "Sign in again to install this download" }
                    state(Transfer("Downloading", 0f))
                    val file = StoreApi(session).download(context, app) { done, total -> state(Transfer("Downloading", done.toFloat() / total)) }
                    // A sign-out or replacement enrollment must not install an in-flight download.
                    check(SessionVault(context).load() == session) { "Sign in again to install this download" }
                    if (app.packageName == context.packageName) {
                        state(Transfer("Waiting for other installs"))
                        while (queue.hasOther(context.packageName) || context.packageManager.packageInstaller.mySessions.any { it.appPackageName != context.packageName }) {
                            delay(500)
                            check(SessionVault(context).load() == session) { "Sign in again to install this download" }
                        }
                    }
                    currentCoroutineContext().ensureActive()
                    check(!background || context.getSharedPreferences("updates", Context.MODE_PRIVATE).getBoolean("auto", true)) { "Automatic updates are turned off" }
                    state(Transfer("Verifying"))
                    Installer.install(context, file, app, background)
                    state(Transfer("Waiting for Android", active = false))
                }
            }
        } catch (e: CancellationException) { mutable.update { it - app.packageName }; throw e }
        catch (e: Exception) { state(Transfer(e.message ?: "Download failed. Tap to retry.", active = false)); throw e }
    }
}
