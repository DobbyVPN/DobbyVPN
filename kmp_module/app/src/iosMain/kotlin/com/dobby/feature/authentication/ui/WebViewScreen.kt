package com.dobby.feature.authentication.ui

import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.runtime.*
import androidx.compose.ui.Modifier
import androidx.compose.ui.interop.UIKitView
import kotlinx.cinterop.ExperimentalForeignApi
import kotlinx.cinterop.ObjCSignatureOverride
import kotlinx.cinterop.readValue
import platform.CoreGraphics.CGRectZero
import platform.Foundation.NSURL
import platform.Foundation.NSURLRequest
import platform.WebKit.WKNavigation
import platform.WebKit.WKNavigationDelegateProtocol
import platform.WebKit.WKWebView
import platform.WebKit.WKWebViewConfiguration
import platform.WebKit.javaScriptEnabled
import platform.darwin.NSObject

@OptIn(ExperimentalForeignApi::class)
@Composable
actual fun WebViewScreen(
    url: String,
    modifier: Modifier,
    enableJavaScript: Boolean
) {
    var isLoading by remember { mutableStateOf(true) }

    Column(modifier = modifier.fillMaxSize()) {
        Box(modifier = Modifier.fillMaxSize()) {
            UIKitView(
                factory = {
                    val config = WKWebViewConfiguration().apply {
                        preferences.javaScriptEnabled = enableJavaScript
                    }
                    WKWebView(frame = CGRectZero.readValue(), configuration = config).apply {
                        navigationDelegate = object : NSObject(), WKNavigationDelegateProtocol {

                            @ObjCSignatureOverride
                            override fun webView(webView: WKWebView, didStartProvisionalNavigation: WKNavigation?) {
                                isLoading = true
                            }

                            @ObjCSignatureOverride
                            override fun webView(webView: WKWebView, didFinishNavigation: WKNavigation?) {
                                isLoading = false
                            }
                        }
                        loadRequest(NSURLRequest(NSURL(string = url)))
                    }
                },
                modifier = Modifier.fillMaxSize()
            )

            if (isLoading) {
                LoadingScreen()
            }
        }
    }
}
