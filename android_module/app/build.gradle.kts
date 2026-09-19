import java.io.File
import java.util.Properties

plugins {
    id("com.android.application")
    id("org.jetbrains.kotlin.android")
}

val repoRoot = rootProject.projectDir.parentFile
val goModule = repoRoot.resolve("go_module")
val goBinary = providers.environmentVariable("GO_BIN").orElse("go")
fun nonBlankEnvironment(name: String) =
    providers.environmentVariable(name)
        .map(String::trim)
        .filter { it.isNotEmpty() }

val localSdkRoot = providers.provider {
    val properties = Properties()
    val localProperties = rootProject.projectDir.resolve("local.properties")
    if (localProperties.isFile) {
        localProperties.inputStream().use { stream -> properties.load(stream) }
    }
    properties.getProperty("sdk.dir").orEmpty()
}
val androidSdkRoot = nonBlankEnvironment("ANDROID_SDK_ROOT")
    .orElse(nonBlankEnvironment("ANDROID_HOME"))
    .orElse(localSdkRoot.map(String::trim).filter { it.isNotEmpty() })
val versionName = providers.gradleProperty("android.injected.version.name")
    .orElse(providers.gradleProperty("versionName")).get()
val versionCode = providers.gradleProperty("android.injected.version.code")
    .orElse(providers.gradleProperty("versionCode")).map(String::toInt).get()
val sourceCommit = providers.gradleProperty("projectRepositoryCommit").getOrElse("N/A")

android {
    namespace = "com.dobby.vpn"
    compileSdk = 35
    // Keep the release APK and its instrumented companion as one tested
    // variant.  Without this explicit selection AGP does not register the
    // assembleReleaseAndroidTest task for the plain application module.
    testBuildType = "release"

    defaultConfig {
        applicationId = providers.gradleProperty("packageName").get()
        minSdk = 26
        targetSdk = 35
        this.versionCode = versionCode
        this.versionName = versionName
        testInstrumentationRunner = "androidx.test.runner.AndroidJUnitRunner"
        manifestPlaceholders["dobbyTestSourceSha"] = sourceCommit
        buildConfigField("String", "PROJECT_REPOSITORY_COMMIT", "\"$sourceCommit\"")
        buildConfigField(
            "String",
            "PROJECT_REPOSITORY_COMMIT_LINK",
            "\"https://github.com/DobbyVPN/DobbyVPN/tree/$sourceCommit\"",
        )
    }

    buildTypes {
        release {
            isMinifyEnabled = false
            proguardFiles(getDefaultProguardFile("proguard-android-optimize.txt"), "proguard-rules.pro")
        }
    }

    // Fyne 2.8.1 supplies a generic notification receiver as generated Java.
    // DobbyVPN does not schedule Fyne notifications; its only notification is
    // the native VPN foreground notification owned by DobbyVpnService. The
    // generated receiver has no Android 13 runtime-permission branch, so its
    // NotificationPermission warning is not actionable in this app.
    lint {
        disable += "NotificationPermission"
    }

    compileOptions {
        sourceCompatibility = JavaVersion.VERSION_17
        targetCompatibility = JavaVersion.VERSION_17
    }
    kotlinOptions { jvmTarget = "17" }

    sourceSets["main"].jniLibs.srcDir(layout.buildDirectory.dir("generated/go-libs"))
    sourceSets["main"].java.srcDir(layout.buildDirectory.dir("generated/fyne-java"))

    buildFeatures { buildConfig = true }
}

val downloadGoModules by tasks.registering(Exec::class) {
    commandLine(goBinary.get(), "mod", "download")
    workingDir(goModule)
}

val copyFyneJava by tasks.registering(Copy::class) {
    val goCache = providers.environmentVariable("GOMODCACHE").orElse(
        providers.provider { File(System.getProperty("user.home"), "go/pkg/mod").absolutePath }
    )
    val fyneRoot = File(goCache.get()).resolve("fyne.io/fyne/v2@v2.8.1/internal/driver/mobile/app")
    val generatedFyneJava = layout.buildDirectory.dir("generated/fyne-java/org/golang/app")
    dependsOn(downloadGoModules)
    from(fyneRoot) { include("GoNativeActivity.java", "FyneNotificationReceiver.java") }
    into(generatedFyneJava)
    rename { it }
    doFirst {
        check(fyneRoot.isDirectory) { "pinned Fyne Java sources are unavailable: $fyneRoot" }
        // Go module cache files are read-only by design. Gradle preserves that
        // mode while copying, so clear the generated output before the release
        // APK and test-companion builds invoke this task again.
        generatedFyneJava.get().asFile.deleteRecursively()
    }
}

val buildGoUI by tasks.registering {
    val ndkHome = nonBlankEnvironment("ANDROID_NDK_HOME")
        .orElse(nonBlankEnvironment("ANDROID_NDK_ROOT"))
        .orElse(androidSdkRoot.map { File(it, "ndk/27.3.13750724").absolutePath })
        .orElse("")
    val api = providers.gradleProperty("android.ndk.api").orElse("26")
    val abis = mapOf(
        "arm64-v8a" to ("arm64" to "aarch64-linux-android"),
        "x86_64" to ("amd64" to "x86_64-linux-android"),
    )
    val outputFiles = abis.map { (androidAbi, _) ->
        layout.buildDirectory.file("generated/go-libs/$androidAbi/libdobby_vpn.so").get().asFile
    }
    inputs.files(fileTree(goModule) { include("**/*.go", "go.mod", "go.sum") })
    outputs.files(outputFiles)
    dependsOn(copyFyneJava)
    doLast {
        check(ndkHome.get().isNotBlank()) {
            "ANDROID_NDK_HOME (or ANDROID_NDK_ROOT) is required to build the Go Android UI"
        }
        val ndk = File(ndkHome.get())
        val toolchain = ndk.resolve("toolchains/llvm/prebuilt")
            .listFiles()?.singleOrNull()
            ?: error("Android NDK LLVM toolchain is unavailable under $ndk")
        val apiLevel = api.get()
        abis.forEach { (androidAbi, pair) ->
            val (goArch, triple) = pair
            val output = layout.buildDirectory.dir("generated/go-libs/$androidAbi").get().asFile
                .resolve("libdobby_vpn.so")
            output.parentFile.mkdirs()
            val compiler = toolchain.resolve("bin/${triple}${apiLevel}-clang")
            check(compiler.isFile) { "Android NDK compiler is unavailable: $compiler" }
            val command = listOf(
                goBinary.get(), "build", "-buildmode=c-shared", "-tags=android,accessibility,static",
                "-trimpath", "-ldflags=-buildid=", "-o", output.absolutePath, "./cmd/dobbyui"
            )
            // Gradle's Exec task is intentionally one process per ABI. Running
            // the same Go command sequentially keeps generated c-shared
            // outputs deterministic and avoids concurrent writes to the Go
            // build cache.
            project.exec {
                commandLine(command)
                workingDir(goModule)
                environment("GOOS", "android")
                environment("GOARCH", goArch)
                environment("CGO_ENABLED", "1")
                environment("CC", compiler.absolutePath)
                // Keep cgo/linker metadata stable across the two clean release
                // builds used by the reproducibility gate.
                environment("SOURCE_DATE_EPOCH", "0")
            }
        }
    }
}

tasks.named("preBuild") { dependsOn(buildGoUI) }

// F-Droid's Gradle output discovery looks below the selected Gradle root
// (android_module/build), while AGP normally writes this app to app/build.
// Keep the normal app output for the Android driver and mirror the unsigned
// release APK at the root only when that task completes.
tasks.named("assembleRelease") {
    doLast {
        val built = layout.buildDirectory.file("outputs/apk/release/app-release-unsigned.apk").get().asFile
        if (built.isFile) {
            val fdroidOutput = rootProject.projectDir.resolve("build/outputs/apk/release")
            fdroidOutput.mkdirs()
            built.copyTo(fdroidOutput.resolve(built.name), overwrite = true)
        }
    }
}

dependencies {
    implementation("androidx.core:core-ktx:1.15.0")
    androidTestImplementation("androidx.test:runner:1.6.2")
    androidTestImplementation("androidx.test.ext:junit:1.2.1")
    androidTestImplementation("androidx.test.uiautomator:uiautomator:2.3.0")
    testImplementation("junit:junit:4.13.2")
}
