package dev.jeeth.privatestore

import android.content.Context
import android.util.Log
import androidx.compose.ui.test.assertIsNotEnabled
import androidx.compose.ui.test.junit4.createAndroidComposeRule
import androidx.compose.ui.test.onNodeWithText
import androidx.compose.ui.test.performClick
import androidx.compose.ui.test.performScrollTo
import androidx.compose.ui.test.performTextReplacement
import androidx.test.ext.junit.runners.AndroidJUnit4
import androidx.test.platform.app.InstrumentationRegistry
import androidx.work.OneTimeWorkRequestBuilder
import androidx.work.WorkInfo
import androidx.work.WorkManager
import org.junit.Assert.*
import org.junit.Assume.assumeTrue
import org.junit.Rule
import org.junit.Test
import org.junit.runner.RunWith
import java.util.concurrent.TimeUnit

@RunWith(AndroidJUnit4::class)
class LauncherInstallationTest {
    @get:Rule val compose = createAndroidComposeRule<MainActivity>()

    @Test fun recognizesInstalledLauncherAndSkipsReinstallation() {
        val args = InstrumentationRegistry.getArguments()
        assumeTrue("Use tools/e2e/launcher-install.py", args.getString("launcherInstallE2E") == "true")
        val context = InstrumentationRegistry.getInstrumentation().targetContext
        assertTrue(context.packageName.endsWith(".testbed"))
        val vault = SessionVault(context)
        val prefs = context.getSharedPreferences("updates", Context.MODE_PRIVATE)
        prefs.edit().putBoolean("auto", false).commit()
        var api: StoreApi? = null
        try {
            compose.onNodeWithText("Use an invitation or another store").performScrollTo().performClick()
            compose.onNodeWithText("Store address").performTextReplacement(args.getString("serverUrl")!!)
            compose.onNodeWithText("Invitation").performTextReplacement(args.getString("invite")!!)
            compose.onNodeWithText("Device name").performTextReplacement("Launcher installation regression")
            compose.onNodeWithText("Enroll this device").performScrollTo().performClick()
            compose.waitUntil(30_000) { vault.load() != null }
            api = StoreApi(requireNotNull(vault.load()))
            val app = api.apps().single()
            assertEquals("app.still.launcher", app.packageName)
            val release = requireNotNull(app.release)
            assertNull(Installer.installed(context, app.packageName))
            compose.onNodeWithText("Install").performScrollTo().performClick()
            Log.i("LauncherInstallE2E", "INSTALL_REQUESTED")
            // Wait for the actual PackageInstaller callback, then query from the store's UID.
            compose.waitUntil(180_000) { prefs.getString("status", "") == "${app.name} installed successfully" }
            assertEquals("Installed launcher must be visible to the store", release.versionCode, Installer.installed(context, app.packageName))
            fun assertCurrent() {
                compose.onNodeWithText("Up to date · ${release.versionName}").performScrollTo().assertExists()
                compose.onNodeWithText("Installed").assertIsNotEnabled()
            }
            assertCurrent()
            compose.onNodeWithText("Check for updates").performScrollTo().performClick()
            compose.waitForIdle()
            assertCurrent()
            compose.activityRule.scenario.recreate()
            compose.waitUntil(30_000) { runCatching { assertCurrent() }.isSuccess }
            val duplicate = api.download(context, app)
            try {
                assertThrows(IllegalArgumentException::class.java) { Installer.verify(context, duplicate, app) }
            } finally { duplicate.delete() }
            prefs.edit().putBoolean("auto", true).commit()
            val worker = OneTimeWorkRequestBuilder<UpdateWorker>().build()
            val manager = WorkManager.getInstance(context)
            manager.enqueue(worker).result.get(10, TimeUnit.SECONDS)
            val deadline = System.nanoTime() + TimeUnit.SECONDS.toNanos(45)
            while (System.nanoTime() < deadline && manager.getWorkInfoById(worker.id).get()?.state?.isFinished != true) Thread.sleep(200)
            assertEquals(WorkInfo.State.SUCCEEDED, manager.getWorkInfoById(worker.id).get()?.state)
            assertEquals(release.versionCode, Installer.installed(context, app.packageName))
            assertFalse(context.packageManager.packageInstaller.mySessions.any { it.appPackageName == app.packageName })
            assertCurrent()
            Log.i("LauncherInstallE2E", "CURRENT_AFTER_INSTALL_REFRESH_RECREATE_AND_WORKER:${release.versionCode}")
        } finally {
            runCatching { api?.revoke() }
            vault.clear()
            UpdateWorker.cancel(context)
        }
    }
}
