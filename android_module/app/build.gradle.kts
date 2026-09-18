import java.io.File

plugins {
    id("com.android.application")
    id("org.jetbrains.kotlin.android")
}

val repoRoot = rootProject.projectDir.parentFile
val goModule = repoRoot.resolve("go_module")
val versionName = providers.gradleProperty("android.injected.version.name")
    .orElse(providers.gradleProperty("versionName")).get()
val versionCode = providers.gradleProperty("android.injected.version.code")
    .orElse(providers.gradleProperty("versionCode")).map(String::toInt).get()
val sourceCommit = providers.gradleProperty("projectRepositoryCommit").getOrElse("N/A")

android {
    namespace = "com.dobby.vpn"
    compileSdk = 35

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

    compileOptions {
        sourceCompatibility = JavaVersion.VERSION_17
        targetCompatibility = JavaVersion.VERSION_17
    }
    kotlinOptions { jvmTarget = "17" }

    sourceSets["main"].jniLibs.srcDir(layout.buildDirectory.dir("generated/go-libs"))
    sourceSets["main"].java.srcDir(layout.buildDirectory.dir("generated/fyne-java"))

    buildFeatures { buildConfig = true }
}

val copyFyneJava by tasks.registering(Copy::class) {
    val goCache = providers.environmentVariable("GOMODCACHE").orElse(
        providers.provider { File(System.getProperty("user.home"), "go/pkg/mod").absolutePath }
    )
    val fyneRoot = File(goCache.get()).resolve("fyne.io/fyne/v2@v2.8.1/internal/driver/mobile/app")
    from(fyneRoot) { include("GoNativeActivity.java", "FyneNotificationReceiver.java") }
    into(layout.buildDirectory.dir("generated/fyne-java/org/golang/app"))
    rename { it }
    doFirst { check(fyneRoot.isDirectory) { "pinned Fyne Java sources are unavailable: $fyneRoot" } }
}

val buildGoUI by tasks.registering {
    val goBinary = providers.environmentVariable("GO_BIN").orElse("go")
    val ndkHome = providers.environmentVariable("ANDROID_NDK_HOME")
        .orElse(providers.environmentVariable("ANDROID_NDK_ROOT"))
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
            }
        }
    }
}

tasks.named("preBuild") { dependsOn(buildGoUI) }

dependencies {
    implementation("androidx.core:core-ktx:1.15.0")
    androidTestImplementation("androidx.test:runner:1.6.2")
    androidTestImplementation("androidx.test.ext:junit:1.2.1")
    androidTestImplementation("androidx.test.uiautomator:uiautomator:2.3.0")
    testImplementation("junit:junit:4.13.2")
}
