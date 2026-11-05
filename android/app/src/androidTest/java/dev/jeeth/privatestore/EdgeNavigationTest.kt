package dev.jeeth.privatestore

import android.content.Intent
import android.os.ParcelFileDescriptor
import android.os.SystemClock
import android.view.InputDevice
import android.view.MotionEvent
import android.view.WindowInsets
import android.view.inputmethod.InputMethodManager
import androidx.test.ext.junit.runners.AndroidJUnit4
import androidx.test.platform.app.InstrumentationRegistry
import org.junit.Assert.*
import org.junit.Test
import org.junit.runner.RunWith

@RunWith(AndroidJUnit4::class)
class EdgeNavigationTest {
    private val instrumentation = InstrumentationRegistry.getInstrumentation()
    private val c = instrumentation.targetContext
    private fun shell(command: String) = ParcelFileDescriptor.AutoCloseInputStream(
        instrumentation.uiAutomation.executeShellCommand(command)).bufferedReader().use { it.readText() }
    private fun ready(message: String, condition: () -> Boolean) {
        val deadline = System.nanoTime() + 30_000_000_000L
        while (!condition() && System.nanoTime() < deadline) Thread.sleep(100)
        assertTrue(message + "\n" + shell("dumpsys activity service com.android.systemui/.SystemUIService"), condition())
    }
    private fun ime(host: EdgeNavigationFixture): Boolean {
        var visible = false
        instrumentation.runOnMainSync { visible = host.window.decorView.rootWindowInsets?.isVisible(WindowInsets.Type.ime()) == true }
        return visible
    }
    private fun await(expected: String, host: EdgeNavigationFixture) {
        val deadline = System.nanoTime() + 15_000_000_000L
        var value = ""
        do {
            instrumentation.runOnMainSync { value = host.destination }
            if (value == expected) return
            Thread.sleep(50)
        } while (System.nanoTime() < deadline)
        assertEquals("Focused=${host.hasWindowFocus()}, IME=${ime(host)}", expected, value)
    }
    private fun swipe(right: Boolean, cancel: Boolean = false) {
        val metrics = c.resources.displayMetrics
        val start = if (right) metrics.widthPixels - 2f else 1f
        val end = if (right) metrics.widthPixels * .55f else metrics.widthPixels * .45f
        val y = metrics.heightPixels * .45f
        val down = SystemClock.uptimeMillis()
        fun event(action: Int, x: Float) {
            val pointer = MotionEvent.PointerProperties().apply {
                id = 0
                toolType = MotionEvent.TOOL_TYPE_FINGER
            }
            val position = MotionEvent.PointerCoords().apply {
                this.x = x
                this.y = y
                pressure = if (action == MotionEvent.ACTION_UP) 0f else 1f
                size = 1f
            }
            val event = MotionEvent.obtain(down, SystemClock.uptimeMillis(), action, 1,
                arrayOf(pointer), arrayOf(position), 0, 0, 1f, 1f, 0, 0, InputDevice.SOURCE_TOUCHSCREEN, 0)
            // Hardware does not wait for the app to render between touch
            // samples. Synchronous injection can turn a swipe into a long press
            // on a busy software-rendered emulator and cancel Android Back.
            try { assertTrue(instrumentation.uiAutomation.injectInputEvent(event, false)) }
            finally { event.recycle() }
        }
        event(MotionEvent.ACTION_DOWN, start)
        for (step in 1..16) {
            SystemClock.sleep(16)
            event(MotionEvent.ACTION_MOVE, start + (end - start) * step / 16f)
        }
        if (cancel) for (step in 1..16) {
            SystemClock.sleep(16)
            event(MotionEvent.ACTION_MOVE, end + (start - end) * step / 16f)
        }
        event(MotionEvent.ACTION_UP, if (cancel) start else end)
    }

    @Test fun actualOsEdgesCancellationBranchingAndKeyboard() {
        org.junit.Assume.assumeTrue(android.os.Build.VERSION.SDK_INT >= 34)
        shell("dumpsys deviceidle unforce")
        shell("input keyevent KEYCODE_WAKEUP")
        shell("wm dismiss-keyguard")
        shell("input keyevent KEYCODE_HOME")
        val originalMode = shell("settings get secure navigation_mode").trim()
        shell("cmd overlay enable-exclusive --category --user 0 com.android.internal.systemui.navbar.gestural")
        ready("Gesture navigation must be enabled") { shell("settings get secure navigation_mode").trim() == "2" }
        ready("SystemUI edge-gesture handler must be ready") {
            shell("dumpsys activity service com.android.systemui/.SystemUIService")
                .split("EdgeBackGestureHandler:").drop(1).any { section ->
                    val status = section.lineSequence().take(12).joinToString("\n")
                    status.contains("mIsEnabled=true") && status.contains("mIsGestureHandlingEnabled=true")
                }
        }
        val host = instrumentation.startActivitySync(Intent(c, EdgeNavigationFixture::class.java)
            .addFlags(Intent.FLAG_ACTIVITY_NEW_TASK)) as EdgeNavigationFixture
        try {
            ready("Fixture window must be focused") { host.hasWindowFocus() }
            instrumentation.runOnMainSync { host.window.insetsController?.hide(WindowInsets.Type.ime()) }
            ready("Keyboard must be hidden before navigation checks") { !ime(host) }
            instrumentation.runOnMainSync { host.visit("List"); host.visit("Details") }
            instrumentation.waitForIdleSync(); Thread.sleep(500)
            swipe(false); await("List", host)
            swipe(true); await("Details", host)
            swipe(false, cancel=true); Thread.sleep(500); await("Details", host)
            swipe(false); await("List", host)
            swipe(false); await("Home", host)
            swipe(true); await("List", host)
            instrumentation.runOnMainSync { host.visit("New branch") }
            swipe(true); Thread.sleep(500); await("New branch", host)
            instrumentation.runOnMainSync {
                host.editor.requestFocus()
                (host.getSystemService(android.content.Context.INPUT_METHOD_SERVICE) as InputMethodManager)
                    .showSoftInput(host.editor, InputMethodManager.SHOW_IMPLICIT)
            }
            ready("Keyboard must be visible for keyboard precedence check") { ime(host) }
            swipe(false)
            ready("Back must dismiss the keyboard") { !ime(host) }
            await("New branch", host)
            shell("input keyevent KEYCODE_BACK"); await("List", host)
        } finally {
            instrumentation.runOnMainSync { host.finish() }
            if (originalMode != "2") shell("cmd overlay enable-exclusive --category --user 0 com.android.internal.systemui.navbar.threebutton")
        }
    }
}
