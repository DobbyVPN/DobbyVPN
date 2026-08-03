package com.dobby.feature.authentication.ui

import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.padding
import androidx.compose.material3.Text
import androidx.compose.runtime.*
import androidx.compose.ui.Alignment
import androidx.compose.ui.ExperimentalComposeUiApi
import androidx.compose.ui.Modifier
import androidx.compose.ui.unit.dp
import androidx.compose.ui.viewinterop.UIKitInteropInteractionMode
import androidx.compose.ui.viewinterop.UIKitInteropProperties
import androidx.compose.ui.viewinterop.UIKitView
import kotlinx.cinterop.ExperimentalForeignApi
import kotlinx.cinterop.ObjCSignatureOverride
import kotlinx.cinterop.readValue
import platform.CoreGraphics.CGRectZero
import platform.Foundation.NSError
import platform.Foundation.NSURL
import platform.Foundation.NSURLRequest
import platform.WebKit.WKNavigation
import platform.WebKit.WKNavigationDelegateProtocol
import platform.WebKit.WKWebView
import platform.WebKit.WKWebViewConfiguration
import platform.darwin.NSObject

@OptIn(ExperimentalComposeUiApi::class, ExperimentalForeignApi::class)
@Composable
actual fun WebViewScreen(
    url: String,
    modifier: Modifier,
    enableJavaScript: Boolean
) {
    var isLoading by remember { mutableStateOf(true) }
    var errorDescription by remember { mutableStateOf<String?>(null) }
    val webNavigationDelegate = remember {
        object : NSObject(), WKNavigationDelegateProtocol {
            @ObjCSignatureOverride
            override fun webView(webView: WKWebView, didStartProvisionalNavigation: WKNavigation?) {
                errorDescription = null
                isLoading = true
            }

            @ObjCSignatureOverride
            override fun webView(webView: WKWebView, didFinishNavigation: WKNavigation?) {
                isLoading = false
            }

            @ObjCSignatureOverride
            override fun webView(
                webView: WKWebView,
                didFailProvisionalNavigation: WKNavigation?,
                withError: NSError
            ) {
                showLoadError(withError)
            }

            @ObjCSignatureOverride
            override fun webView(webView: WKWebView, didFailNavigation: WKNavigation?, withError: NSError) {
                showLoadError(withError)
            }

            private fun showLoadError(error: NSError) {
                errorDescription = error.localizedDescription
                isLoading = false
            }
        }
    }

    Column(modifier = modifier.fillMaxSize()) {
        Box(modifier = Modifier.fillMaxSize()) {
            key(url, enableJavaScript) {
                UIKitView(
                    factory = {
                        val config = WKWebViewConfiguration().apply {
                            defaultWebpagePreferences.allowsContentJavaScript = enableJavaScript
                        }
                        WKWebView(frame = CGRectZero.readValue(), configuration = config).apply {
                            navigationDelegate = webNavigationDelegate
                            loadRequest(NSURLRequest(NSURL(string = url)))
                        }
                    },
                    modifier = Modifier.fillMaxSize(),
                    onRelease = { webView ->
                        webView.stopLoading()
                        webView.navigationDelegate = null
                    },
                    // Authentication pages contain editable native controls. Preserve immediate
                    // touch delivery and native accessibility when moving to Compose 1.7 interop.
                    properties = UIKitInteropProperties(
                        interactionMode = UIKitInteropInteractionMode.NonCooperative,
                        isNativeAccessibilityEnabled = true
                    )
                )
            }

            if (isLoading && errorDescription == null) {
                LoadingScreen()
            }
            errorDescription?.let { description ->
                Column(
                    modifier = Modifier
                        .fillMaxSize()
                        .padding(24.dp),
                    verticalArrangement = Arrangement.Center,
                    horizontalAlignment = Alignment.CenterHorizontally
                ) {
                    Text(text = "Failed to load page")
                    Text(text = description)
                }
            }
        }
    }
}
