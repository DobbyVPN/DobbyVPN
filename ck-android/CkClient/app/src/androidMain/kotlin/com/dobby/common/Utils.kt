package com.dobby.common

import android.content.Context
import android.widget.Toast

fun Context.showToast(message: String, duration: Int) {
    Toast.makeText(this, message, duration).show()
}