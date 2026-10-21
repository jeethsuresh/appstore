package dev.jeeth.privatestore

import android.Manifest
import android.app.NotificationChannel
import android.app.NotificationManager
import android.app.PendingIntent
import android.content.BroadcastReceiver
import android.content.Context
import android.content.Intent
import android.content.pm.PackageInstaller
import android.content.pm.PackageManager
import android.os.Build
import androidx.core.app.NotificationCompat
import androidx.core.content.ContextCompat
import java.io.File
import java.security.MessageDigest

object Installer {
    fun installed(context: Context, packageName: String): Long? = try {
        val info = context.packageManager.getPackageInfo(packageName, 0)
        if (Build.VERSION.SDK_INT >= 28) info.longVersionCode else @Suppress("DEPRECATION") info.versionCode.toLong()
    } catch (_: PackageManager.NameNotFoundException) { null }

    @Suppress("DEPRECATION")
    fun verifyArchive(context: Context, file: File, app: StoreApp) {
        val release = requireNotNull(app.release)
        val flags = if (Build.VERSION.SDK_INT >= 28) PackageManager.GET_SIGNING_CERTIFICATES else PackageManager.GET_SIGNATURES
        val archive = context.packageManager.getPackageArchiveInfo(file.path, flags) ?: error("Android could not read the APK")
        val code = if (Build.VERSION.SDK_INT >= 28) archive.longVersionCode else archive.versionCode.toLong()
        require(archive.packageName == app.packageName && code == release.versionCode && archive.versionName == release.versionName) { "APK identity or version mismatch" }
        val signatures = if (Build.VERSION.SDK_INT >= 28) archive.signingInfo?.apkContentsSigners else archive.signatures
        val certs = signatures?.map { MessageDigest.getInstance("SHA-256").digest(it.toByteArray()).toHex() }
        require(Versions.validDigest(app.certificate) && certs == listOf(app.certificate)) { "APK release certificate mismatch" }
        require(Build.VERSION.SDK_INT >= release.minSdk) { "This app needs a newer Android version" }
    }
    fun verify(context: Context, file: File, app: StoreApp) {
        verifyArchive(context, file, app)
        require(Versions.newer(installed(context, app.packageName), requireNotNull(app.release).versionCode)) { "This version is already installed or older" }
    }
    @Synchronized
    fun install(context: Context, file: File, app: StoreApp, background: Boolean) {
        verify(context, file, app)
        require(!background || installed(context, app.packageName) != null) { "App was removed before the update finished" }
        require(context.packageManager.canRequestPackageInstalls()) { "Allow Private Store to install apps in Android settings" }
        val installer = context.packageManager.packageInstaller
        val prefs = context.getSharedPreferences("updates", Context.MODE_PRIVATE)
        if (background && prefs.getLong("pending.${app.packageName}", 0) == app.release!!.versionCode && installer.mySessions.any { it.appPackageName == app.packageName }) { file.delete(); return }
        // Avoid accumulating sessions while the user is considering a previous confirmation.
        installer.mySessions.filter { it.appPackageName == app.packageName }.forEach { installer.abandonSession(it.sessionId) }
        val params = PackageInstaller.SessionParams(PackageInstaller.SessionParams.MODE_FULL_INSTALL).apply {
            setAppPackageName(app.packageName); setSize(file.length())
            if (Build.VERSION.SDK_INT >= 31) setRequireUserAction(if (background) PackageInstaller.SessionParams.USER_ACTION_NOT_REQUIRED else PackageInstaller.SessionParams.USER_ACTION_REQUIRED)
        }
        val id = installer.createSession(params)
        try {
            installer.openSession(id).use { session ->
                file.inputStream().use { input -> session.openWrite("base.apk", 0, file.length()).use { output -> input.copyTo(output); session.fsync(output) } }
                val callback = Intent(context, InstallReceiver::class.java).setAction("dev.jeeth.privatestore.INSTALL_RESULT").putExtra("appName", app.name).putExtra("packageName", app.packageName).putExtra("versionCode", app.release!!.versionCode)
                val flags = PendingIntent.FLAG_UPDATE_CURRENT or (if (Build.VERSION.SDK_INT >= 31) PendingIntent.FLAG_MUTABLE else 0)
                val pending = PendingIntent.getBroadcast(context, id, callback, flags)
                session.commit(pending.intentSender)
                context.getSharedPreferences("updates", Context.MODE_PRIVATE).edit().putLong("pending.${app.packageName}", app.release.versionCode).apply()
            }
        } catch (e: Exception) { installer.abandonSession(id); throw e }
        finally { file.delete() }
    }
}
object Notices {
    fun enabled(context: Context): Boolean {
        val manager = context.getSystemService(NotificationManager::class.java)
        return manager.areNotificationsEnabled() && manager.getNotificationChannel("updates")?.importance != NotificationManager.IMPORTANCE_NONE && (Build.VERSION.SDK_INT < 33 || ContextCompat.checkSelfPermission(context, Manifest.permission.POST_NOTIFICATIONS) == PackageManager.PERMISSION_GRANTED)
    }
    fun show(context: Context, id: Int, title: String, body: String, intent: Intent = Intent(context, MainActivity::class.java)) {
        val manager = context.getSystemService(NotificationManager::class.java)
        manager.createNotificationChannel(NotificationChannel("updates", "App updates", NotificationManager.IMPORTANCE_DEFAULT))
        if (!enabled(context)) return
        val pending = PendingIntent.getActivity(context, id, intent, PendingIntent.FLAG_UPDATE_CURRENT or PendingIntent.FLAG_IMMUTABLE)
        try { manager.notify(id, NotificationCompat.Builder(context, "updates").setSmallIcon(android.R.drawable.stat_sys_download_done).setContentTitle(title).setContentText(body).setContentIntent(pending).setAutoCancel(true).build()) } catch (_: SecurityException) { /* Permission can change while a worker runs. */ }
    }
}
class InstallReceiver : BroadcastReceiver() {
    @Suppress("DEPRECATION")
    override fun onReceive(context: Context, intent: Intent) {
        val id = intent.getIntExtra(PackageInstaller.EXTRA_SESSION_ID, 0)
        val status = intent.getIntExtra(PackageInstaller.EXTRA_STATUS, PackageInstaller.STATUS_FAILURE)
        val name = intent.getStringExtra("appName") ?: "App"
        val pkg = intent.getStringExtra("packageName") ?: return
        val prefs = context.getSharedPreferences("updates", Context.MODE_PRIVATE)
        if (status == PackageInstaller.STATUS_PENDING_USER_ACTION) {
            val confirm = if (Build.VERSION.SDK_INT >= 33) intent.getParcelableExtra(Intent.EXTRA_INTENT, Intent::class.java) else intent.getParcelableExtra(Intent.EXTRA_INTENT)
            confirm ?: return
            if (MainActivity.visible) {
                try { context.startActivity(confirm.addFlags(Intent.FLAG_ACTIVITY_NEW_TASK)); return } catch (_: Exception) { /* Offer a notification when background launch is restricted. */ }
            }
            Notices.show(context, id, "Finish installing $name", "Tap to approve the Android installation.", confirm)
        } else {
            prefs.edit().remove("pending.$pkg").putString("status", if (status == PackageInstaller.STATUS_SUCCESS) "$name installed successfully" else "$name installation was cancelled or failed; tap Install to retry").apply()
            Notices.show(context, id, name, prefs.getString("status", "")!!)
        }
    }
}
