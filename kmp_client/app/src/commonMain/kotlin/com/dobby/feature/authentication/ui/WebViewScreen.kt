package com.dobby.feature.authentication.ui

import androidx.compose.runtime.Composable
import androidx.compose.ui.Modifier

@Composable
expect fun WebViewScreen(
    url: String = "https://www.google.com",
    modifier: Modifier = Modifier,
    enableJavaScript: Boolean = true
)