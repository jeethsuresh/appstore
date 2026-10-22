package dev.jeeth.privatestore

import android.net.Uri
import androidx.compose.ui.test.junit4.createEmptyComposeRule
import androidx.compose.ui.test.onNodeWithText
import androidx.compose.ui.test.performClick
import androidx.compose.ui.test.performScrollTo
import androidx.compose.ui.test.performTextReplacement
import androidx.test.ext.junit.runners.AndroidJUnit4
import androidx.test.platform.app.InstrumentationRegistry
import androidx.test.runner.lifecycle.ActivityLifecycleMonitorRegistry
import androidx.test.runner.lifecycle.Stage
import java.io.FileInputStream
import org.json.JSONObject
import org.junit.Assert.*
import org.junit.Assume.assumeTrue
import org.junit.Rule
import org.junit.Test
import org.junit.runner.RunWith
import java.net.HttpURLConnection
import java.net.URL

@RunWith(AndroidJUnit4::class)
class GravitasE2ETest {
    @get:Rule val compose = createEmptyComposeRule()
    @Test fun realGravitasSignInSurvivesRecreationAndRevokes() {
        val args = InstrumentationRegistry.getArguments()
        assumeTrue("Use tools/e2e/native-login.py on an isolated emulator", args.getString("nativeGravitasE2E") == "true")
        val context = InstrumentationRegistry.getInstrumentation().targetContext
        assertTrue(context.packageName.endsWith(".testbed"))
        val vault = SessionVault(context)
        fun open(data: Uri? = null) {
            // A test process in the background cannot start activities on modern
            // Android. Bring the task forward as the emulator user would.
            require(data == null || data.toString().none { it.isWhitespace() })
            val command = "am start -W -n ${context.packageName}/dev.jeeth.privatestore.MainActivity " +
                if (data != null) "-a android.intent.action.VIEW -d ${data}" else "-a android.intent.action.MAIN"
            InstrumentationRegistry.getInstrumentation().uiAutomation.executeShellCommand(command).use {
                FileInputStream(it.fileDescriptor).use { stream -> stream.readBytes() }
            }
            compose.waitUntil(10_000) { MainActivity.visible }
        }
        try {
            open();compose.waitForIdle()
            assertNull(vault.load())
            compose.onNodeWithText("Device name").performTextReplacement("Gravitas E2E emulator")
            compose.onNodeWithText("Sign in with Gravitas").performScrollTo().performClick()
            compose.waitUntil(30_000) { vault.pending() != null && !MainActivity.visible }
            val first = requireNotNull(vault.pending())
            open();compose.waitForIdle()
            compose.onNodeWithText("Cancel sign-in").performScrollTo().performClick()
            assertNull(vault.pending());assertNull(vault.load())
            compose.onNodeWithText("Sign in with Gravitas").performScrollTo().performClick()
            compose.waitUntil(30_000) { vault.pending() != null && !MainActivity.visible }
            val pending = requireNotNull(vault.pending())
            assertNotEquals(first.state,pending.state)
            open();compose.waitForIdle()
            val instrumentation = InstrumentationRegistry.getInstrumentation()
            var previous: MainActivity? = null
            instrumentation.runOnMainSync {
                previous = ActivityLifecycleMonitorRegistry.getInstance().getActivitiesInStage(Stage.RESUMED).filterIsInstance<MainActivity>().single()
                previous!!.recreate()
            }
            compose.waitUntil(10_000) { previous!!.isDestroyed && MainActivity.visible };compose.waitForIdle()
            assertEquals(pending,SessionVault(context).pending())
            val stored = context.getSharedPreferences("gravitas-pending",0).getString("data","")!!
            assertFalse(stored.contains(pending.verifier))
            // The host harness signs in through the real IAM browser endpoints. It only
            // sees this public URL (state + challenge); the verifier stays on the phone.
            val conn = URL(args.getString("controlUrl")!! + "/authorize").openConnection() as HttpURLConnection
            val returned: Uri
            try {
                conn.requestMethod="POST";conn.connectTimeout=20_000;conn.readTimeout=120_000;conn.doOutput=true
                conn.setRequestProperty("Content-Type","application/json")
                conn.outputStream.use { it.write(JSONObject().put("authorizationUrl",pending.authorizationUrl).toString().toByteArray()) }
                assertEquals(200,conn.responseCode)
                returned=Uri.parse(JSONObject(conn.inputStream.bufferedReader().use { it.readText() }).getString("callback"))
            } finally { conn.disconnect() }
            val wrong = returned.buildUpon().clearQuery().appendQueryParameter("code",returned.getQueryParameter("code")).appendQueryParameter("state","x".repeat(43)).build()
            open(wrong);compose.waitForIdle()
            assertNull(vault.load());assertEquals(pending,vault.pending())
            open(returned)
            compose.waitUntil(45_000) { vault.load()?.account?.isNotBlank() == true && vault.pending() == null }
            val session=requireNotNull(vault.load())
            assertEquals(session,SessionVault(context).load());assertNull(vault.pending())
            val api=StoreApi(session);val apps=api.apps()
            assertEquals(setOf("daybreak","spacetime"),apps.map { it.id }.toSet())
            val app=apps.single { it.id=="daybreak" };val apk=api.download(context,app)
            Installer.verifyArchive(context,apk,app);apk.delete()
            api.revoke()
            try { api.apps();fail("Revoked Gravitas device accepted") } catch (e:ApiException) { assertEquals(401,e.code) }
        } finally {
            open()
            runCatching { vault.load()?.let { StoreApi(it).revoke() } }
            vault.clear();UpdateWorker.cancel(context)
            InstrumentationRegistry.getInstrumentation().runOnMainSync {
                ActivityLifecycleMonitorRegistry.getInstance().getActivitiesInStage(Stage.RESUMED).filterIsInstance<MainActivity>().forEach { it.finish() }
            }
        }
    }
}
