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
    @Test fun gravitasPendingProofSurvivesReloadAndIsEncrypted() {
        val context = InstrumentationRegistry.getInstrumentation().targetContext
        val vault = SessionVault(context)
        val pending = GravitasLogin.create(GravitasLogin.STORE, "Test phone").copy(authorizationUrl = "https://gravitas.cool/oauth/start")
        try {
            vault.savePending(pending)
            assertEquals(pending, SessionVault(context).pending())
            val encrypted = context.getSharedPreferences("gravitas-pending", 0).getString("data", "")!!
            assertFalse(encrypted.contains(pending.verifier));assertFalse(encrypted.contains(pending.state))
            val account = Session(GravitasLogin.STORE,"test-token","@reader:gravitas.cool")
            vault.save(account);assertEquals(account,SessionVault(context).load())
            vault.clearPending();assertNull(vault.pending());assertEquals(account,vault.load())
        } finally { vault.clear() }
    }
    @Test fun enrollmentScreenLaunches() {
        val instrumentation = InstrumentationRegistry.getInstrumentation()
        val activity = instrumentation.startActivitySync(Intent(instrumentation.targetContext, MainActivity::class.java).addFlags(Intent.FLAG_ACTIVITY_NEW_TASK))
        instrumentation.waitForIdleSync()
        assertFalse(activity.isFinishing)
        instrumentation.runOnMainSync { activity.finish() }
    }
}
