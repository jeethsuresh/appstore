package dev.jeeth.privatestore

import android.content.Intent
import androidx.test.ext.junit.runners.AndroidJUnit4
import androidx.test.platform.app.InstrumentationRegistry
import org.junit.Assert.*
import org.junit.Test
import org.junit.runner.RunWith

@RunWith(AndroidJUnit4::class)
class DeviceSmokeTest {
    @Test fun encryptedEnrollmentSurvivesReloadAndCanBeCleared() {
        val context = InstrumentationRegistry.getInstrumentation().targetContext
        val vault = SessionVault(context)
        val session = Session("https://store.example.com", "test-device-token")
        try {
            vault.save(session)
            assertEquals(session, SessionVault(context).load())
            val ciphertext = context.getSharedPreferences("session", 0).getString("data", "")!!
            assertFalse(ciphertext.contains(session.token))
            assertTrue((Installer.installed(context, context.packageName) ?: 0L) > 0L)
        } finally { vault.clear() }
        assertNull(vault.load())
    }
    @Test fun enrollmentScreenLaunches() {
        val instrumentation = InstrumentationRegistry.getInstrumentation()
        val activity = instrumentation.startActivitySync(Intent(instrumentation.targetContext, MainActivity::class.java).addFlags(Intent.FLAG_ACTIVITY_NEW_TASK))
        instrumentation.waitForIdleSync()
        assertFalse(activity.isFinishing)
        instrumentation.runOnMainSync { activity.finish() }
    }
}
