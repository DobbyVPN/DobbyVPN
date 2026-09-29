# The Compose Activity is referenced by the manifest. The Go JNI entry points
# and Kotlin VPN callbacks use fixed class and method names.
-keep class com.dobby.ui.** { *; }
-keep class com.dobby.nativebridge.** { *; }
