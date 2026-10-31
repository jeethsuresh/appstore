package dev.jeeth.privatestore

import android.os.ParcelFileDescriptor
import android.os.PowerManager
import androidx.test.ext.junit.runners.AndroidJUnit4
import androidx.test.platform.app.InstrumentationRegistry
import org.junit.Assert.*
import org.junit.Assume.assumeTrue
import org.junit.Test
import org.junit.runner.RunWith

/** Runs only in the isolated testbed package on a disposable emulator. */
@RunWith(AndroidJUnit4::class)
class NotificationRelayPowerTest {
    private fun shell(command: String): String = ParcelFileDescriptor.AutoCloseInputStream(
        InstrumentationRegistry.getInstrumentation().uiAutomation.executeShellCommand(command),
    ).bufferedReader().use { it.readText() }

    private fun await(description: String, condition: () -> Boolean) {
        val until = System.nanoTime() + 10_000_000_000L
        while (System.nanoTime() < until) {
            if (condition()) return
            Thread.sleep(100)
        }
        fail("Timed out: $description")
    }

    @Test fun screenOffDozeHonorsGrantAndRevocation() {
        val context = InstrumentationRegistry.getInstrumentation().targetContext
        assumeTrue("Use -PstoreTestBuildType=e2e on a disposable emulator", context.packageName.endsWith(".testbed"))
        val pkg = context.packageName
        val power = context.getSystemService(PowerManager::class.java)
        val originallyExempt = NotificationRelay.ignoresBatteryOptimizations(context)
        try {
            shell("dumpsys deviceidle whitelist -$pkg")
            await("battery optimization enabled") { !NotificationRelay.ignoresBatteryOptimizations(context) }
            shell("dumpsys battery unplug")
            shell("input keyevent KEYCODE_SLEEP")
            shell("dumpsys deviceidle force-idle")
            await("screen off in Doze") { !power.isInteractive && power.isDeviceIdleMode }
            assertFalse("Non-exempt relay must wait in Doze", NotificationRelay.canConnect(context, networkValidated = true))

            shell("dumpsys deviceidle whitelist +$pkg")
            await("battery exemption granted") { NotificationRelay.ignoresBatteryOptimizations(context) }
            assertTrue("Exempt relay must be allowed in Doze", NotificationRelay.canConnect(context, networkValidated = true))
            assertFalse("Exemption must not retry offline", NotificationRelay.canConnect(context, networkValidated = false))

            shell("dumpsys deviceidle whitelist -$pkg")
            await("battery exemption revoked") { !NotificationRelay.ignoresBatteryOptimizations(context) }
            assertFalse("Revocation must restore queued Doze delivery", NotificationRelay.canConnect(context, networkValidated = true))

            shell("dumpsys deviceidle unforce")
            shell("input keyevent KEYCODE_WAKEUP")
            await("awake after Doze") { power.isInteractive && !power.isDeviceIdleMode }
            assertTrue("Awake relay must not require an exemption", NotificationRelay.canConnect(context, networkValidated = true))
        } finally {
            shell("dumpsys deviceidle unforce")
            shell("dumpsys battery reset")
            shell("input keyevent KEYCODE_WAKEUP")
            shell("dumpsys deviceidle whitelist ${if (originallyExempt) "+" else "-"}$pkg")
        }
    }
}
