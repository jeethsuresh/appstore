package dev.jeeth.privatestore

import android.app.Activity
import android.os.Bundle
import android.widget.EditText
import android.widget.LinearLayout
import android.widget.TextView
import cool.gravitas.navigation.GravitasEdgeNavigation
import cool.gravitas.navigation.NavigationHistory

/** Debug-only, offline fixture for the shared OS gesture bridge. */
class EdgeNavigationFixture : Activity() {
    private val history = NavigationHistory("Home")
    private lateinit var label: TextView
    lateinit var editor: EditText
    val destination get() = history.current
    override fun onCreate(state: Bundle?) {
        super.onCreate(state)
        window.setSoftInputMode(android.view.WindowManager.LayoutParams.SOFT_INPUT_STATE_ALWAYS_HIDDEN)
        label = TextView(this).apply { textSize = 28f; text = history.current }
        editor = EditText(this).apply { hint = "Keyboard precedence" }
        setContentView(LinearLayout(this).apply { orientation = LinearLayout.VERTICAL; isFocusableInTouchMode = true; addView(label); addView(editor); requestFocus() })
        GravitasEdgeNavigation.install(this, { back() }, { history.forward(); render() })
    }
    fun visit(value: String) { history.visit(value); render() }
    private fun render() { label.text = history.current }
    private fun back() { history.back(); render() }
    @Deprecated("Hardware Back is intentionally supported by this fixture")
    override fun onBackPressed() = back()
}
