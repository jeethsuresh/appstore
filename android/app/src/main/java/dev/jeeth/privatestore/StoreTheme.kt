package dev.jeeth.privatestore

import androidx.compose.foundation.isSystemInDarkTheme
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.darkColorScheme
import androidx.compose.material3.lightColorScheme
import androidx.compose.runtime.Composable
import androidx.compose.ui.graphics.Color

@Composable
fun StoreTheme(content: @Composable () -> Unit) {
    MaterialTheme(
        colorScheme = if (isSystemInDarkTheme()) darkColorScheme(
            primary = Color(0xFFBAC3FF), background = Color(0xFF111318), surface = Color(0xFF191C22)
        ) else lightColorScheme(
            primary = Color(0xFF344DCE), background = Color(0xFFF5F6FA), surface = Color.White
        ),
        content = content
    )
}
