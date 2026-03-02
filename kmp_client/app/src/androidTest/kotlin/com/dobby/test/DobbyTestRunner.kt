package com.dobby.test

import android.app.Application
import android.content.Context
import androidx.test.runner.AndroidJUnitRunner

class DobbyTestRunner : AndroidJUnitRunner() {
    override fun newApplication(cl: ClassLoader?, className: String?, context: Context?): Application {
        return super.newApplication(cl, TestDobbyApp::class.java.name, context)
    }
}
