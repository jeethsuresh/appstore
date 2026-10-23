package dev.jeeth.privatestore

import android.content.Context
import android.content.Intent
import android.util.Log
import androidx.test.ext.junit.runners.AndroidJUnit4
import androidx.test.platform.app.InstrumentationRegistry
import androidx.work.OneTimeWorkRequestBuilder
import androidx.work.WorkInfo
import androidx.work.WorkManager
import org.junit.Assert.*
import org.junit.Assume.assumeTrue
import org.junit.Test
import org.junit.Rule
import androidx.compose.ui.test.junit4.createAndroidComposeRule
import androidx.compose.ui.test.onNodeWithText
import androidx.compose.ui.test.onAllNodesWithText
import androidx.compose.ui.test.performTextInput
import androidx.compose.ui.test.performTextReplacement
import androidx.compose.ui.test.performClick
import androidx.compose.ui.test.performScrollTo
import androidx.compose.ui.test.onFirst
import org.junit.runner.RunWith
import java.util.concurrent.TimeUnit

@RunWith(AndroidJUnit4::class)
class DistributionE2ETest {
    @get:Rule val compose = createAndroidComposeRule<MainActivity>()
    private fun stage(value: String) { Log.i("StoreE2E", "${InstrumentationRegistry.getArguments().getString("runId")}:$value") }
    private fun await(description: String, seconds: Long = 180, condition: () -> Boolean) {
        val end = System.nanoTime() + TimeUnit.SECONDS.toNanos(seconds)
        while (System.nanoTime() < end) { if (condition()) return; Thread.sleep(300) }
        fail("Timed out: $description")
    }
    @Test fun enrollDownloadInstallUpdateAndRevoke() {
        val args = InstrumentationRegistry.getArguments()
        assumeTrue("Run using tools/e2e/run.py and the isolated e2e build", args.getString("distributionE2E") == "true")
        val instrumentation = InstrumentationRegistry.getInstrumentation()
        val context = instrumentation.targetContext
        assertTrue(context.packageName.endsWith(".testbed"))
        val vault = SessionVault(context)
        vault.clear()
        compose.onNodeWithText("Use an invitation or another store").performScrollTo().performClick()
        compose.onNodeWithText("Store address").performTextReplacement(args.getString("serverUrl")!!)
        compose.onNodeWithText("Invitation").performTextInput(args.getString("invite")!!)
        compose.onNodeWithText("Device name").performTextReplacement("Distribution E2E emulator")
        compose.onNodeWithText("Enroll this device").performScrollTo().performClick()
        compose.waitUntil(30_000) { vault.load() != null }
        val session = requireNotNull(vault.load())
        assertEquals(session, SessionVault(context).load())
        stage("ENROLLED_THROUGH_UI")
        val api = StoreApi(session)
        val apps = api.apps()
        assertTrue(apps.map { it.id }.containsAll(listOf("daybreak", "spacetime", "appstore")))
        val spacetime = apps.single { it.id == "spacetime" }
        val spacetimeFile = api.download(context, spacetime)
        Installer.verifyArchive(context, spacetimeFile, spacetime)
        spacetimeFile.delete()
        stage("SPACETIME_DOWNLOAD_VERIFIED")
        val baseline = apps.single { it.id == "daybreak" }
        assertEquals(6L, baseline.release!!.versionCode)
        assertNull(Installer.installed(context, baseline.packageName))
        val activity = compose.activity
        compose.waitUntil(30_000) { compose.onAllNodesWithText("Daybreak").fetchSemanticsNodes().isNotEmpty() }
        stage("READY_FOR_INITIAL_INSTALL")
        compose.onAllNodesWithText("Install").onFirst().performScrollTo().performClick()
        await("initial Android installation") { Installer.installed(context, baseline.packageName) == 6L }
        stage("BASELINE_INSTALLED")
        instrumentation.runOnMainSync { activity.finish() }
        await("new release ingestion") { api.apps().single { it.id == "daybreak" }.release!!.versionCode == 7L }
        context.getSharedPreferences("updates", Context.MODE_PRIVATE).edit().putBoolean("auto", true).commit()
        assertTrue(Notices.enabled(context))
        val request = OneTimeWorkRequestBuilder<UpdateWorker>().build()
        val manager = WorkManager.getInstance(context)
        manager.enqueue(request).result.get(10, TimeUnit.SECONDS)
        stage("BACKGROUND_UPDATE_ENQUEUED")
        await("periodic-worker update") { Installer.installed(context, baseline.packageName) == 7L }
        await("worker completion",30) { manager.getWorkInfoById(request.id).get()?.state == WorkInfo.State.SUCCEEDED }
        assertTrue(context.getSharedPreferences("updates", Context.MODE_PRIVATE).getLong("lastCheck",0) > 0)
        stage("BACKGROUND_UPDATE_INSTALLED")
        api.revoke()
        try { api.apps(); fail("Revoked device was accepted") } catch (e: ApiException) { assertEquals(401,e.code) }
        vault.clear(); UpdateWorker.cancel(context)
        stage("DEVICE_REVOKED")
    }
}
