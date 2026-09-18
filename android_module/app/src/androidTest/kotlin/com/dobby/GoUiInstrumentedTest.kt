package com.dobby

import android.content.Intent
import androidx.test.ext.junit.runners.AndroidJUnit4
import androidx.test.platform.app.InstrumentationRegistry
import androidx.test.uiautomator.By
import androidx.test.uiautomator.UiDevice
import androidx.test.uiautomator.Until
import org.junit.Assert.assertNotNull
import org.junit.Test
import org.junit.runner.RunWith

/** Real-renderer smoke: the Go/Fyne activity must expose stable controls. */
@RunWith(AndroidJUnit4::class)
class GoUiInstrumentedTest {
    @Test
    fun connectionControlsAreVisibleAndSettingsRoundTrips() {
        val instrumentation = InstrumentationRegistry.getInstrumentation()
        val device = UiDevice.getInstance(instrumentation)
        val packageName = instrumentation.targetContext.packageName
        device.pressHome()
        val launch = instrumentation.targetContext.packageManager
            .getLaunchIntentForPackage(packageName)
            ?.addFlags(Intent.FLAG_ACTIVITY_CLEAR_TASK or Intent.FLAG_ACTIVITY_NEW_TASK)
        requireNotNull(launch) { "Go/Fyne launcher activity is missing" }
        instrumentation.targetContext.startActivity(launch)
        device.wait(Until.hasObject(By.pkg(packageName)), 10_000)

        assertNotNull(device.findObject(By.text("Disconnected")))
        assertNotNull(device.findObject(By.text("Connect")))
        val settings = device.findObject(By.text("Settings"))
        assertNotNull(settings)
        settings.click()
        device.wait(Until.hasObject(By.text("Back")), 2_000)
        device.findObject(By.text("Back"))?.click()
        assertNotNull(device.findObject(By.text("Connect")))
    }
}
