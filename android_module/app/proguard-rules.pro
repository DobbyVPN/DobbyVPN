# The Go/Fyne activity and the native bridge are referenced by Android's
# manifest/JNI names. Keep their public entry points in release builds.
-keep class org.golang.app.** { *; }
-keep class com.dobby.nativebridge.** { *; }
