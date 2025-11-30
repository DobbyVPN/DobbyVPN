package com.dobby.feature.authentication.ui

import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.material3.CircularProgressIndicator
import androidx.compose.runtime.Composable
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import com.dobby.feature.authentication.domain.HideConfigsManager
import com.dobby.navigation.LoadingScreen
import com.dobby.navigation.WebViewScreen

@Composable
fun AuthenticationScreen(
    onNavigate: (Any) -> Unit = {},
    screen: Any,
) {
    onNavigate.invoke(LoadingScreen)
    HideConfigsManager.authenticate(
        onSuccess = {
            onNavigate.invoke(screen)
        }, onFailure = {
            onNavigate.invoke(WebViewScreen)
        }
    )
}

@Composable
fun LoadingScreen() {
    Box(
        modifier = Modifier
            .fillMaxSize(),
        contentAlignment = Alignment.Center
    ) {
        CircularProgressIndicator()
    }
}