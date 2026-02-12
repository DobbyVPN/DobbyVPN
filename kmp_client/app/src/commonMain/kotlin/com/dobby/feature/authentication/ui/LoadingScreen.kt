package com.dobby.feature.authentication.ui

import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.material3.CircularProgressIndicator
import androidx.compose.runtime.Composable
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.navigation.NavController
import com.dobby.feature.authentication.domain.HideConfigsManager
import com.dobby.navigation.LoadingScreen
import com.dobby.navigation.MainScreen
import com.dobby.navigation.SettingsScreen
import com.dobby.navigation.WebViewScreen

@Composable
fun AuthenticationScreen(
    screen: Any,
    navController: NavController
) {
    navController.navigate(LoadingScreen) {
        popUpTo(SettingsScreen) { inclusive = true }
        popUpTo(MainScreen) { inclusive = true }
    }

    HideConfigsManager.authenticate(
        onSuccess = {
            navController.navigate(screen) {
                popUpTo(LoadingScreen) { inclusive = true }
            }
        }, onFailure = {
            navController.navigate(WebViewScreen) {
                popUpTo(LoadingScreen) { inclusive = true }
            }
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