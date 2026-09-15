import org.gradle.kotlin.dsl.implementation
import org.jetbrains.kotlin.gradle.ExperimentalKotlinGradlePluginApi
import org.jetbrains.kotlin.gradle.dsl.JvmTarget
import java.io.File
import java.util.Properties

data class AndroidNativeAbi(
    val androidName: String,
    val gomobileArch: String,
    val ndkTriple: String,
)

// Keep the gomobile AAR and the libc++ runtime payload in lockstep. The hosted
// Android emulator is x86_64, while production devices remain arm64.
val androidNativeAbis = listOf(
    AndroidNativeAbi("arm64-v8a", "arm64", "aarch64-linux-android"),
    AndroidNativeAbi("x86_64", "amd64", "x86_64-linux-android"),
)

val repoRoot: File = rootProject.projectDir.parentFile
val goModuleDir: File = repoRoot.resolve("go_module")
val gomobileAar = layout.buildDirectory.file("generated/gomobile/dobbyvpn-runtime.aar")
val gomobileExecutable = providers.gradleProperty("gomobileExecutable")
    .orElse(providers.environmentVariable("GOMOBILE"))
    .orElse(providers.provider {
        val userHomeExecutable = File(System.getProperty("user.home"), "go/bin/gomobile")
        if (userHomeExecutable.canExecute()) userHomeExecutable.absolutePath else "gomobile"
    })
val goCacheDir = providers.environmentVariable("DOBBYVPN_GOMOBILE_GOCACHE")
    .map(::File)
    .orElse(layout.buildDirectory.dir("go-cache").map { it.asFile })
val goTmpDir = providers.environmentVariable("DOBBYVPN_GOMOBILE_GOTMPDIR")
    .map(::File)
    .orElse(layout.buildDirectory.dir("go-tmp").map { it.asFile })
val generatedJniLibsDir = layout.buildDirectory.dir("generated/jniLibs")
val goRootDir = providers.gradleProperty("gomobileGoRoot")
    .orElse(providers.environmentVariable("GOROOT"))
val androidSdkDir = providers.gradleProperty("gomobileAndroidSdkRoot")
    .orElse(providers.environmentVariable("ANDROID_HOME"))
    .orElse(providers.environmentVariable("ANDROID_SDK_ROOT"))
    .orElse(providers.provider {
        val localProperties = rootProject.projectDir.resolve("local.properties")
        if (!localProperties.isFile) {
            return@provider ""
        }
        val properties = Properties()
        localProperties.inputStream().use(properties::load)
        properties.getProperty("sdk.dir").orEmpty()
    })
val androidNdkDir = providers.gradleProperty("gomobileAndroidNdkHome")
    .orElse(providers.environmentVariable("ANDROID_NDK_HOME"))
    .orElse(providers.environmentVariable("ANDROID_NDK_ROOT"))
    .orElse(providers.provider {
        val sdkDir = androidSdkDir.get()
        if (sdkDir.isBlank()) {
            return@provider ""
        }
        val ndkRoot = File(sdkDir, "ndk")
        val preferredNdk = ndkRoot.resolve("27.3.13750724")
        when {
            preferredNdk.isDirectory -> preferredNdk.absolutePath
            ndkRoot.isDirectory -> ndkRoot.listFiles()
                ?.filter { it.isDirectory }
                ?.maxByOrNull { it.name }
                ?.absolutePath
                .orEmpty()
            else -> ""
        }
    })
val backendGomobileAar = files(gomobileAar).builtBy(":app:gomobileBindAndroid")

plugins {
    alias(libs.plugins.android.application)
    alias(libs.plugins.composeMultiplatform)
    alias(libs.plugins.compose.compiler)
    alias(libs.plugins.kotlinMultiplatform)
    alias(libs.plugins.hydraulic.conveyor)

    id("com.github.gmazzo.buildconfig") version "5.6.5"
}

version = "1.0"

java {
    toolchain {
        languageVersion.set(JavaLanguageVersion.of(17))
    }
}

kotlin {
    androidTarget {
        @OptIn(ExperimentalKotlinGradlePluginApi::class)
        compilerOptions {
            jvmTarget.set(JvmTarget.JVM_17)
        }
    }

    jvm {
        compilerOptions {
            jvmTarget.set(JvmTarget.JVM_17)
        }
    }

    listOf(
        iosArm64(),
        iosX64(),
        iosSimulatorArm64(),
    ).forEach { target ->
        target.binaries.framework {
            baseName = "app"
            isStatic = true
            binaryOption("bundleId", "vpn.dobby.app.shared")
        }
    }

    sourceSets {

        androidMain.dependencies {
            implementation(libs.androidx.activity.compose)
            implementation(libs.androidx.core.ktx)
            implementation(libs.androidx.lifecycle.runtime.ktx)
            implementation(libs.androidx.ui)
            implementation(libs.androidx.ui.graphics)
            implementation(libs.androidx.material3)

            implementation(backendGomobileAar)


        }

        commonMain.dependencies {
            implementation(compose.runtime)
            implementation(compose.foundation)
            implementation(compose.material3)
            implementation(compose.ui)
            implementation(compose.components.resources)
            implementation(compose.components.uiToolingPreview)
            implementation(libs.kotlinx.serialization.json)
            implementation(libs.lifecycle.viewmodel.compose)
            implementation(libs.okio)

            implementation(libs.lifecycle.viewmodel)

        }

        commonTest.dependencies {
            implementation(kotlin("test"))
        }

        @OptIn(org.jetbrains.compose.ExperimentalComposeLibrary::class)
        iosTest.dependencies {
            implementation(compose.uiTest)
        }

        jvmMain.dependencies {
            implementation(project(":grpcstub"))
            implementation(libs.protobuf.java)

            implementation(compose.desktop.currentOs)
            implementation(libs.skiko.win)
            implementation(libs.skiko.mac.amd64)
            implementation(libs.skiko.mac.arm64)
            implementation(libs.skiko.linux)

            implementation(libs.kotlinx.coroutines.swing)
        }

        jvmTest.dependencies {
            implementation(libs.kotlinx.coroutines.test)
        }

        androidUnitTest.dependencies {
            implementation(kotlin("test-junit"))
            implementation(libs.junit)
        }

        androidInstrumentedTest.dependencies {
            implementation(libs.androidx.junit)
            implementation(libs.androidx.test.runner)
            implementation(libs.androidx.uiautomator)
            implementation(libs.junit)
            implementation(libs.okhttp)
        }

        iosMain.dependencies {

        }
    }
}

compose.desktop {
    application {
        mainClass = "MainKt"
    }
}

android {
    namespace = providers.gradleProperty("packageName").get()
    compileSdk = 35
    testBuildType = "release"

    defaultConfig {
        minSdk = 26
        targetSdk = 35

        applicationId = providers.gradleProperty("packageName").get()
        versionCode = providers.gradleProperty("android.injected.version.code")
            .orElse(providers.gradleProperty("versionCode"))
            .map { it.toInt() }
            .getOrElse(1)

        versionName = providers.gradleProperty("android.injected.version.name")
            .orElse(providers.gradleProperty("versionName"))
            .getOrElse("0.0.1")

        testInstrumentationRunner = "com.dobby.TestApplicationRunner"

        // This placeholder is merged only into the instrumentation APK's
        // manifest.  The Release driver verifies it before signing the
        // companion, proving that the test interface was built from the same
        // exact source identity without adding anything to the production APK.
        manifestPlaceholders["dobbyTestSourceSha"] = providers.gradleProperty("projectRepositoryCommit")
            .getOrElse("N/A")

        vectorDrawables {
            useSupportLibrary = true
        }
    }

    dependenciesInfo {
        // Disables dependency metadata when building APKs.
        includeInApk = false
        // Disables dependency metadata when building Android App Bundles.
        includeInBundle = false
    }


    buildTypes {
        release {
            isMinifyEnabled = false
            proguardFiles(
                getDefaultProguardFile("proguard-android-optimize.txt"),
                "proguard-rules.pro"
            )
        }
    }

    packaging {
        resources {
            excludes += "/META-INF/{AL2.0,LGPL2.1}"
        }
    }

    compileOptions {
        sourceCompatibility = JavaVersion.VERSION_17
        targetCompatibility = JavaVersion.VERSION_17
    }

    sourceSets {
        getByName("main") {
            jniLibs.srcDir(generatedJniLibsDir)
        }
    }
}

val copyLibCxxTask = tasks.register<Sync>("copyLibCxx") {
    val ndkDir = File(androidNdkDir.get())
    val osName = System.getProperty("os.name").lowercase()
    val osArch = System.getProperty("os.arch").lowercase()
    val hostTag = when {
        osName.contains("windows") -> "windows-x86_64"
        osName.contains("mac") -> {
            val armTag = "darwin-x86_64"
            val arm64Tag = "darwin-arm64"
            if (ndkDir.resolve("toolchains/llvm/prebuilt/$arm64Tag").exists()) arm64Tag
            else if (ndkDir.resolve("toolchains/llvm/prebuilt/$armTag").exists()) armTag
            else if (osArch.contains("arm") || osArch.contains("aarch64")) arm64Tag
            else armTag
        }
        else -> "linux-x86_64"
    }
    val libcxxPaths = androidNativeAbis.associateWith { abi ->
        ndkDir.resolve("toolchains/llvm/prebuilt/$hostTag/sysroot/usr/lib/${abi.ndkTriple}/libc++_shared.so")
    }

    androidNativeAbis.forEach { abi ->
        from(libcxxPaths.getValue(abi)) {
            into(abi.androidName)
        }
    }
    into(generatedJniLibsDir)

    doFirst {
        check(ndkDir.isDirectory) {
            "Android NDK is required to package native runtimes: ${ndkDir.absolutePath}"
        }
        libcxxPaths.forEach { (abi, path) ->
            check(path.isFile) {
                "Missing libc++_shared.so for ${abi.androidName}: ${path.absolutePath}"
            }
        }
    }
}

tasks.named("preBuild") {
    dependsOn(copyLibCxxTask)
}

val gomobileBindAndroid by tasks.registering(Exec::class) {
    group = "build"
    description = "Builds the Android Go backend AAR with gomobile."

    val outputFile = gomobileAar.get().asFile
    inputs.files(fileTree(goModuleDir) {
        include("**/*.go")
        exclude("**/build/**")
    })
    inputs.file(goModuleDir.resolve("go.mod"))
    inputs.file(goModuleDir.resolve("go.sum"))
    outputs.file(outputFile)
    val gomobilePath = listOf(
        goRootDir.orNull?.let { File(it, "bin").absolutePath }.orEmpty(),
        File(System.getProperty("user.home"), "go/bin").absolutePath,
        "/usr/local/go/bin",
        System.getenv("PATH").orEmpty()
    ).filter { it.isNotBlank() }.distinct().joinToString(File.pathSeparator)
    val inheritedGoFlags = System.getenv("GOFLAGS").orEmpty()
        .split(Regex("\\s+"))
        .filter { it.isNotBlank() }
        .filterNot { flag ->
            flag == "-trimpath" || flag.startsWith("-trimpath=") ||
                flag == "-buildvcs" || flag.startsWith("-buildvcs=")
        }
    val canonicalGoFlags = (
        inheritedGoFlags + listOf("-trimpath", "-buildvcs=false")
        ).distinct().joinToString(" ")

    doFirst {
        outputFile.parentFile.mkdirs()
        goTmpDir.get().mkdirs()
        logger.lifecycle("gomobileBindAndroid: gomobile=${gomobileExecutable.get()}")
        logger.lifecycle("gomobileBindAndroid: GOROOT=${goRootDir.orNull.orEmpty()}")
        logger.lifecycle("gomobileBindAndroid: PATH=$gomobilePath")
    }

    workingDir = goModuleDir
    commandLine(
        gomobileExecutable.get(),
        "bind",
        "-target=${androidNativeAbis.joinToString(",") { "android/${it.gomobileArch}" }}",
        "-androidapi=26",
        "-tags=static",
        "-javapkg=com.dobby.gomobile",
        "-ldflags=-s -w -buildid=",
        "-o=${outputFile.absolutePath}",
        "go_module/kotlin_exports"
    )
    environment(
        "PATH",
        gomobilePath
    )
    goRootDir.orNull?.takeIf { it.isNotBlank() }?.let {
        environment("GOROOT", it)
    }
    environment("GO111MODULE", "on")
    environment("GOCACHE", goCacheDir.get().absolutePath)
    environment("GOTMPDIR", goTmpDir.get().absolutePath)
    environment("SOURCE_DATE_EPOCH", "0")
    environment("CGO_CFLAGS_ALLOW", ".*")
    environment("CGO_CXXFLAGS_ALLOW", ".*")
    environment("CGO_LDFLAGS_ALLOW", ".*")
    environment(
        "GOFLAGS",
        canonicalGoFlags
    )
    if (androidSdkDir.get().isNotBlank()) {
        environment("ANDROID_HOME", androidSdkDir.get())
        environment("ANDROID_SDK_ROOT", androidSdkDir.get())
    }
    if (androidNdkDir.get().isNotBlank()) {
        val ndkDir = File(androidNdkDir.get())
        val toolchainBin = ndkDir
            .resolve("toolchains/llvm/prebuilt")
            .listFiles()
            ?.firstOrNull { it.isDirectory }
            ?.resolve("bin")

        environment("ANDROID_NDK_HOME", ndkDir.absolutePath)
        environment("ANDROID_NDK_ROOT", ndkDir.absolutePath)
        environment("CGO_ENABLED", "1")

        val isWindows = System.getProperty("os.name").lowercase().contains("windows")
        val debugPrefixFlags = if (isWindows) {
            ""
        } else {
            listOf(
                "-fdebug-prefix-map=${repoRoot.absolutePath}=/src/DobbyVPN",
                "-fdebug-prefix-map=${goModuleDir.absolutePath}=/src/DobbyVPN/go_module",
                "-fdebug-prefix-map=${goTmpDir.get().absolutePath}=/tmp/go-build",
                "-fdebug-prefix-map=${androidSdkDir.get()}=/android-sdk",
                "-fdebug-prefix-map=${ndkDir.absolutePath}=/android-ndk",
                "-ffile-prefix-map=${repoRoot.absolutePath}=/src/DobbyVPN",
                "-ffile-prefix-map=${goModuleDir.absolutePath}=/src/DobbyVPN/go_module",
                "-ffile-prefix-map=${goTmpDir.get().absolutePath}=/tmp/go-build",
                "-ffile-prefix-map=${androidSdkDir.get()}=/android-sdk",
                "-ffile-prefix-map=${ndkDir.absolutePath}=/android-ndk"
            ).joinToString(" ")
        }
        environment("CGO_CFLAGS", listOf(debugPrefixFlags, System.getenv("CGO_CFLAGS").orEmpty()).joinToString(" ").trim())
        environment(
            "CGO_LDFLAGS",
            listOf(debugPrefixFlags, "-Wl,-z,max-page-size=16384", "-lc++_shared", System.getenv("CGO_LDFLAGS").orEmpty())
                .joinToString(" ")
                .trim()
        )

        if (toolchainBin?.isDirectory == true) {
            environment(
                "PATH",
                listOf(
                    toolchainBin.absolutePath,
                    gomobilePath
                ).joinToString(File.pathSeparator)
            )
        }
    }
}

val verifyDebugNativeAbiPayloads by tasks.registering(Exec::class) {
    group = "verification"
    description = "Verifies arm64-v8a and x86_64 Go JNI libraries in the AAR and complete native payloads in the debug APK."

    val debugApk = layout.buildDirectory.file("outputs/apk/debug/app-debug.apk")
    dependsOn(gomobileBindAndroid, copyLibCxxTask, "packageDebug")
    inputs.file(gomobileAar)
    inputs.dir(generatedJniLibsDir)
    inputs.file(debugApk)
    inputs.file(repoRoot.resolve(".github/scripts/verify_android_native_payloads.py"))
    inputs.file(repoRoot.resolve(".github/scripts/bounded_process.py"))

    val readElf = providers.provider {
        val ndkDir = File(androidNdkDir.get())
        val osName = System.getProperty("os.name").lowercase()
        val hostTag = when {
            osName.contains("windows") -> "windows-x86_64"
            osName.contains("mac") && ndkDir.resolve("toolchains/llvm/prebuilt/darwin-arm64").isDirectory -> "darwin-arm64"
            osName.contains("mac") -> "darwin-x86_64"
            else -> "linux-x86_64"
        }
        ndkDir.resolve(
            "toolchains/llvm/prebuilt/$hostTag/bin/llvm-readelf" +
                if (osName.contains("windows")) ".exe" else ""
        )
    }
    val python = providers.environmentVariable("PYTHON")
        .orElse(providers.provider {
            if (System.getProperty("os.name").lowercase().contains("windows")) "python" else "python3"
        })

    doFirst {
        check(readElf.get().canExecute()) {
            "Android NDK llvm-readelf is required to verify Go JNI symbols: ${readElf.get().absolutePath}"
        }
    }
    commandLine(
        python.get(),
        repoRoot.resolve(".github/scripts/verify_android_native_payloads.py").absolutePath,
        "--aar", gomobileAar.get().asFile.absolutePath,
        "--apk", debugApk.get().asFile.absolutePath,
        "--readelf", readElf.get().absolutePath,
    )
}

buildConfig {
    className = "BuildConfig"
    packageName = providers.gradleProperty("packageName").get()

    useKotlinOutput()

    buildConfigField(
        "int",
        "VERSION_CODE",
        providers.gradleProperty("android.injected.version.code")
            .orElse(providers.gradleProperty("versionCode"))
            .map { it.toInt() }
            .getOrElse(1)
    )

    buildConfigField(
        "String",
        "VERSION_NAME",
        "\"${providers.gradleProperty("android.injected.version.name")
            .orElse(providers.gradleProperty("versionName"))
            .getOrElse("0.0.1")}\""
    )

    buildConfigField(
        "String",
        "PROJECT_REPOSITORY_COMMIT",
        "\"${providers.gradleProperty("projectRepositoryCommit").getOrElse("N/A")}\""
    )
    buildConfigField(
        "String",
        "PROJECT_REPOSITORY_COMMIT_LINK",
        "\"${providers.gradleProperty("projectRepositoryCommitLink").getOrElse("N/A")}\""
    )
}


dependencies {
    debugImplementation(libs.androidx.ui.tooling)
    debugImplementation(libs.androidx.ui.test.manifest)
}
