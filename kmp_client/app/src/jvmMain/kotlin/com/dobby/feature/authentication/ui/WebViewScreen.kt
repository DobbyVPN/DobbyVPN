package com.dobby.feature.authentication.ui

import androidx.compose.runtime.Composable
import androidx.compose.ui.Modifier

@Composable
actual fun WebViewScreen(
    url: String,
    modifier: Modifier,
    enableJavaScript: Boolean
) {
    LoadingScreen()
}