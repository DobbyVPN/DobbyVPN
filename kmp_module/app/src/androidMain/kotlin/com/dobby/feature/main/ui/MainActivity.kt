package com.dobby.feature.main.ui

import android.content.Intent
import android.net.VpnService
import android.os.Bundle
import android.view.WindowManager
import androidx.activity.ComponentActivity
import androidx.activity.compose.BackHandler
import androidx.activity.compose.setContent
import androidx.activity.result.ActivityResultLauncher
import androidx.activity.result.contract.ActivityResultContracts.StartActivityForResult
import androidx.lifecycle.lifecycleScope
import com.dobby.common.ui.theme.DobbyTheme
import com.dobby.AppDependenciesProvider
import com.dobby.navigation.App
import kotlinx.coroutines.launch

class MainActivity : ComponentActivity() {

    private lateinit var requestVpnPermissionLauncher: ActivityResultLauncher<Intent>

    private val appDependencies get() = (application as AppDependenciesProvider).appDependencies

    override fun onCreate(savedInstanceState: Bundle?) {
        super.onCreate(savedInstanceState)
        val dependencies = appDependencies

        initVpnPermissionLauncher()
        lifecycleScope.launch {
            dependencies.permissionEventsChannel.checkPermissionsEvents.collect {
                checkVpnPermissionAndStart()
            }
        }
        window?.setFlags(
            WindowManager.LayoutParams.FLAG_SECURE,
            WindowManager.LayoutParams.FLAG_SECURE
        )
        setContent {
            DobbyTheme {
                BackHandler(enabled = dependencies.navigation.canGoBack) {
                    dependencies.navigation.goBack()
                }
                App(dependencies)
            }
        }
    }

    private fun checkVpnPermissionAndStart() {
        val vpnIntent = VpnService.prepare(this)
        if (vpnIntent != null) {
            requestVpnPermissionLauncher.launch(vpnIntent)
        } else {
            onPermissionGranted(isGranted = true)
        }
    }

    private fun initVpnPermissionLauncher() {
        requestVpnPermissionLauncher = registerForActivityResult(
            StartActivityForResult()
        ) { result -> onPermissionGranted(isGranted = result.resultCode == RESULT_OK) }
    }

    private fun onPermissionGranted(isGranted: Boolean) {
        lifecycleScope.launch { appDependencies.permissionEventsChannel.onPermissionGranted(isGranted) }
    }
}
