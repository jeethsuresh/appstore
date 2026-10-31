package dev.jeeth.privatestore

import android.content.Context
import android.graphics.Bitmap
import androidx.activity.compose.setContent
import androidx.compose.material3.*
import androidx.compose.runtime.mutableStateOf
import androidx.compose.ui.test.*
import androidx.compose.ui.test.junit4.createAndroidComposeRule
import androidx.test.ext.junit.runners.AndroidJUnit4
import androidx.test.platform.app.InstrumentationRegistry
import org.junit.Assert.*
import org.junit.Assume.assumeTrue
import org.junit.Rule
import org.junit.Test
import org.junit.runner.RunWith
import java.io.File

@RunWith(AndroidJUnit4::class)
class StoreCatalogTest {
    @get:Rule val compose = createAndroidComposeRule<MainActivity>()
    private val context get() = InstrumentationRegistry.getInstrumentation().targetContext
    private fun screenshot(name: String) {
        val image = InstrumentationRegistry.getInstrumentation().uiAutomation.takeScreenshot()
        File(context.filesDir, "$name.png").outputStream().use { image.compress(Bitmap.CompressFormat.PNG, 100, it) }
        image.recycle()
    }
    private fun app(id: String, name: String, code: Long = 2) = StoreApp(id, name, "dev.example.$id", "a".repeat(64), "$name makes your day a little easier.", StoreRelease(code, code, "1.$code", "b".repeat(64), 8_000_000, 26, "Improved $name with faster startup and a clearer interface."))

    @Test fun catalogSearchDetailsLayoutUpdatesAndProgressSurviveNavigation() {
        assumeTrue(context.packageName.endsWith(".testbed"))
        context.getSharedPreferences("catalog", Context.MODE_PRIVATE).edit().putBoolean("list", false).commit()
        val apps = listOf(app("daybreak", "Daybreak"), app("spacetime", "Spacetime"), app("atlas", "Atlas"), app("river", "River"), app("launcher", "Still Launcher"), app("haven", "Haven"))
        val transfers = mutableStateOf<Map<String, Downloads.Transfer>>(emptyMap())
        var requested = emptyList<StoreApp>()
        compose.runOnUiThread { compose.activity.setContent {
            MaterialTheme { StoreCatalog(apps, mapOf("dev.example.daybreak" to 1L), transfers.value, emptySet(), false, "", {}, { requested = it }) { Text("Settings content") } }
        } }
        compose.onNodeWithText("Daybreak").assertIsDisplayed()
        screenshot("catalog-grid")
        compose.onNodeWithContentDescription("Show list").performClick()
        compose.onNodeWithContentDescription("Show two columns").assertExists()
        compose.onNodeWithText("Search apps").performTextInput("Atlas")
        compose.onNodeWithText("Daybreak").assertDoesNotExist()
        compose.onNodeWithText("Atlas").performClick()
        compose.onNodeWithText("About this app").assertIsDisplayed()
        compose.onNodeWithText("Release notes").performScrollTo().assertIsDisplayed()
        screenshot("app-details")
        compose.onNodeWithContentDescription("Back to apps").performClick()
        compose.onNodeWithText("Search apps").assertTextContains("Atlas")
        compose.onNodeWithText("Search apps").performTextClearance()
        compose.onNodeWithText("Updates (1)").performClick()
        compose.onNodeWithText("Update all").performClick()
        compose.runOnIdle { assertEquals(listOf(apps.first()), requested); transfers.value = mapOf("dev.example.daybreak" to Downloads.Transfer("Downloading", 0.45f)) }
        compose.onNodeWithText("Downloading").assertIsNotEnabled()
        compose.onNodeWithText("Settings").performClick()
        compose.onNodeWithText("Settings content").assertIsDisplayed()
        compose.onNodeWithText("Apps").performClick()
        compose.onNodeWithText("Downloading").assertIsNotEnabled()
        screenshot("parallel-progress")
    }

    @Test fun realSettingsPersistTogglesAndLocalSignOut() {
        assumeTrue(context.packageName.endsWith(".testbed"))
        val prefs = context.getSharedPreferences("updates", Context.MODE_PRIVATE)
        val originalAuto = prefs.getBoolean("auto", true)
        val vault = SessionVault(context)
        try {
            vault.save(Session("https://127.0.0.1:9", "isolated-ui-fixture", "@fixture:example.invalid"))
            compose.activityRule.scenario.recreate()
            compose.onNodeWithText("Settings").performClick()
            compose.onNodeWithText("Automatic updates").assertIsDisplayed()
            compose.onAllNodes(isToggleable())[0].performClick()
            compose.runOnIdle { assertEquals(!originalAuto, prefs.getBoolean("auto", true)) }
            screenshot("settings")
            compose.activityRule.scenario.recreate()
            compose.onNodeWithText("Settings").performClick()
            compose.onNodeWithText("Automatic updates").assertIsDisplayed()
            compose.runOnIdle { assertEquals(!originalAuto, prefs.getBoolean("auto", true)) }
            compose.waitUntil(30_000) { !compose.onNodeWithText("Forget local enrollment").fetchSemanticsNode().config.contains(androidx.compose.ui.semantics.SemanticsProperties.Disabled) }
            compose.onNodeWithText("Forget local enrollment").performScrollTo().performClick()
            compose.onNodeWithText("Device name").assertExists()
            assertNull(vault.load())
        } finally { vault.clear(); UpdateWorker.cancel(context); prefs.edit().putBoolean("auto", originalAuto).commit() }
    }
}
