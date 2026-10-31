package dev.jeeth.privatestore

import android.content.Context
import android.content.Intent
import android.content.pm.PackageInstaller
import androidx.test.ext.junit.runners.AndroidJUnit4
import androidx.test.platform.app.InstrumentationRegistry
import org.junit.Assert.*
import org.junit.Assume.assumeTrue
import org.junit.Test
import org.junit.runner.RunWith
import java.io.File
import java.security.MessageDigest

@RunWith(AndroidJUnit4::class)
class InstallCacheTest {
    @Test fun androidFailureRetainsTheVerifiedDownloadAndSuccessRemovesIt() {
        val context = InstrumentationRegistry.getInstrumentation().targetContext
        assumeTrue(context.packageName.endsWith(".testbed"))
        val bytes = "isolated callback fixture".toByteArray()
        val digest = MessageDigest.getInstance("SHA-256").digest(bytes).toHex()
        val key = ApkCache.Key("dev.example.callbackfixture", 2, digest)
        val cache = ApkCache(File(context.filesDir, "downloads"))
        val file = cache.obtain(key, bytes.size.toLong()) { it.writeBytes(bytes) }
        val prefs = context.getSharedPreferences("updates", Context.MODE_PRIVATE)
        try {
            prefs.edit().putLong("pending.${key.packageName}", key.version).commit()
            fun result(status: Int) = Intent(context, InstallReceiver::class.java)
                .putExtra("appName", "Isolated fixture").putExtra("packageName", key.packageName).putExtra("versionCode", key.version)
                .putExtra(PackageInstaller.EXTRA_STATUS, status)
            InstallReceiver().onReceive(context, result(PackageInstaller.STATUS_FAILURE_ABORTED))
            assertTrue("Cancellation must retain the APK", file.exists())
            assertFalse(prefs.contains("pending.${key.packageName}"))
            assertEquals(file, cache.obtain(key, bytes.size.toLong()) { fail("Retry must use the saved file") })
            InstallReceiver().onReceive(context, result(PackageInstaller.STATUS_SUCCESS))
            assertFalse("Successful callback must remove the APK", file.exists())
        } finally { file.delete(); prefs.edit().remove("pending.${key.packageName}").remove("status").commit() }
    }
}
