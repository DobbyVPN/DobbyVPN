package com.dobby.backend

class OutlineBackend {
    external fun getLastError(): String?

    external fun newOutlineClient(config: String, fd: Int): Unit

    external fun outlineConnect(): Int

    external fun outlineDisconnect(): Unit

    companion object {
        init {
            System.loadLibrary("backend")
        }
    }
}
