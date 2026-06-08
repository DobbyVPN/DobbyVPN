import org.gradle.kotlin.dsl.implementation
import org.jetbrains.kotlin.gradle.ExperimentalKotlinGradlePluginApi
import org.jetbrains.kotlin.gradle.dsl.JvmTarget
import java.io.File
import java.util.Properties

val repoRoot: File = rootProject.projectDir.parentFile
val goModuleDir: File = repoRoot.resolve("go_module")
val cloakInternalDir: File = repoRoot.resolve("Cloak/internal")
val goModuleCloakInternalDir: File = goModuleDir.resolve("modules/Cloak/internal")
val gomobileAar = layout.buildDirectory.file("generated/gomobile/backend.aar")
val gomobileExecutable = providers.gradleProperty("gomobileExecutable")
    .orElse(providers.environmentVariable("GOMOBILE"))
    .orElse(providers.provider {
        val userHomeExecutable = File(System.getProperty("user.home"), "go/bin/gomobile")
        if (userHomeExecutable.canExecute()) userHomeExecutable.absolutePath else "gomobile"
    })
val goCacheDir = layout.buildDirectory.dir("go-cache")
val goTmpDir = layout.buildDirectory.dir("go-tmp")
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
    alias(libs.plugins.jetbrains.kotlin.serialization)
    alias(libs.plugins.kotlinMultiplatform)
    alias(libs.plugins.hydraulic.conveyor)

    id("com.github.gmazzo.buildconfig") version "5.6.5"
    id("io.sentry.kotlin.multiplatform.gradle") version "0.18.0" apply false
}

version = "1.0"

java {
    toolchain {
        languageVersion.set(JavaLanguageVersion.of(17))
    }
}

// Keep it enabled by default (CI/release), but allow disabling for local Xcode builds via: -PdisableSentry=true
val disableSentry = providers.gradleProperty("disableSentry").orNull?.lowercase() in setOf("1", "true", "yes")
if (!disableSentry) {
    apply(plugin = "io.sentry.kotlin.multiplatform.gradle")
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

    iosArm64().binaries.framework {
        baseName = "app"
        isStatic = true
    }

    sourceSets {

        androidMain.dependencies {
            implementation(compose.preview)
            implementation(libs.androidx.activity.compose)
            implementation(libs.androidx.biometric.ktx)
            implementation(libs.androidx.core.ktx)
            implementation(libs.androidx.lifecycle.runtime.ktx)
            implementation(libs.androidx.ui)
            implementation(libs.androidx.ui.graphics)
            implementation(libs.androidx.ui.tooling.preview)
            implementation(libs.androidx.material3)
            implementation(libs.androidx.compiler)
            implementation(libs.kotlin.script.runtime)
            implementation(libs.koin.android)
            implementation(libs.koin.androidx.compose)

            implementation(backendGomobileAar)

            implementation(libs.okhttp)
            implementation(libs.ktor.client.okhttp)

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
            implementation(libs.navigation.compose)
            implementation(libs.okio)

            api(libs.koin.core)
            implementation(libs.koin.compose)
            implementation(libs.lifecycle.viewmodel)

            implementation(libs.ktor.client.core)
            implementation(libs.ktor.client.content.negotiation)
            implementation(libs.ktor.serialization.kotlinx.json)

            implementation(libs.tomlkt)

            implementation(libs.datetime)

            implementation("com.russhwolf:multiplatform-settings-no-arg:1.3.0")

            implementation(compose.components.resources)
        }

        jvmMain.dependencies {
            implementation(project(":grpcstub"))
            runtimeOnly(libs.grpc.netty)

            implementation(compose.desktop.currentOs)
            implementation(libs.skiko.win)
            implementation(libs.skiko.mac.amd64)
            implementation(libs.skiko.mac.arm64)
            implementation(libs.skiko.linux)

            implementation(libs.kotlinx.coroutines.swing)
            implementation(libs.jna)
            implementation(libs.gson)
            implementation(libs.ktor.client.cio)
        }

        iosMain.dependencies {

            implementation(libs.ktor.client.darwin)

            implementation(libs.compass.geocoder.mobile)
            implementation(libs.compass.geolocation.mobile)
            implementation(libs.compass.permissions.mobile)
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
}

val gomobileBindAndroid by tasks.registering(Exec::class) {
    group = "build"
    description = "Builds the Android Go backend AAR with gomobile."

    val outputFile = gomobileAar.get().asFile
    inputs.files(fileTree(goModuleDir) {
        include("**/*.go")
        exclude("**/build/**")
    })
    inputs.dir(cloakInternalDir).optional()
    inputs.file(goModuleDir.resolve("go.mod"))
    inputs.file(goModuleDir.resolve("go.sum"))
    outputs.file(outputFile)
    val gomobilePath = listOf(
        goRootDir.orNull?.let { File(it, "bin").absolutePath }.orEmpty(),
        File(System.getProperty("user.home"), "go/bin").absolutePath,
        "/usr/local/go/bin",
        System.getenv("PATH").orEmpty()
    ).filter { it.isNotBlank() }.distinct().joinToString(File.pathSeparator)

    doFirst {
        check(cloakInternalDir.isDirectory) {
            "Cloak submodule is not initialized: missing ${cloakInternalDir.absolutePath}"
        }
        outputFile.parentFile.mkdirs()
        goTmpDir.get().asFile.mkdirs()
        copy {
            from(cloakInternalDir)
            into(goModuleCloakInternalDir)
        }
        logger.lifecycle("gomobileBindAndroid: gomobile=${gomobileExecutable.get()}")
        logger.lifecycle("gomobileBindAndroid: GOROOT=${goRootDir.orNull.orEmpty()}")
        logger.lifecycle("gomobileBindAndroid: PATH=$gomobilePath")
    }

    workingDir = goModuleDir
    commandLine(
        gomobileExecutable.get(),
        "bind",
        "-target=android/arm64",
        "-androidapi=26",
        "-javapkg=com.dobby.gomobile",
        "-trimpath",
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
    environment("GOCACHE", goCacheDir.get().asFile.absolutePath)
    environment("GOTMPDIR", goTmpDir.get().asFile.absolutePath)
    environment("SOURCE_DATE_EPOCH", "0")
    environment(
        "GOFLAGS",
        listOf("-trimpath", "-buildvcs=false", System.getenv("GOFLAGS").orEmpty())
            .joinToString(" ")
            .trim()
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
        environment("CC", "aarch64-linux-android26-clang")
        environment("CXX", "aarch64-linux-android26-clang++")

        val debugPrefixFlags = listOf(
            "-fdebug-prefix-map=${repoRoot.absolutePath}=/src/DobbyVPN",
            "-fdebug-prefix-map=${goModuleDir.absolutePath}=/src/DobbyVPN/go_module",
            "-fdebug-prefix-map=${goTmpDir.get().asFile.absolutePath}=/tmp/go-build",
            "-fdebug-prefix-map=${androidSdkDir.get()}=/android-sdk",
            "-fdebug-prefix-map=${ndkDir.absolutePath}=/android-ndk",
            "-ffile-prefix-map=${repoRoot.absolutePath}=/src/DobbyVPN",
            "-ffile-prefix-map=${goModuleDir.absolutePath}=/src/DobbyVPN/go_module",
            "-ffile-prefix-map=${goTmpDir.get().asFile.absolutePath}=/tmp/go-build",
            "-ffile-prefix-map=${androidSdkDir.get()}=/android-sdk",
            "-ffile-prefix-map=${ndkDir.absolutePath}=/android-ndk"
        ).joinToString(" ")
        environment("CGO_CFLAGS", listOf(debugPrefixFlags, System.getenv("CGO_CFLAGS").orEmpty()).joinToString(" ").trim())
        environment(
            "CGO_LDFLAGS",
            listOf("-Wl,-z,max-page-size=16384", System.getenv("CGO_LDFLAGS").orEmpty())
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
    implementation(project(":grpcstub"))
    debugImplementation(libs.androidx.ui.tooling)
    debugImplementation(libs.androidx.ui.test.manifest)
}
