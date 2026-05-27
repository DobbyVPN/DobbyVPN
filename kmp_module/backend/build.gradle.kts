@file:Suppress("UnstableApiUsage")

import org.jetbrains.kotlin.gradle.tasks.KotlinCompile
import java.io.File
import java.util.Properties

plugins {
    alias(libs.plugins.android.library)
    alias(libs.plugins.kotlin.android)
}

val pkg: String = "com.dobby.backend"
val repoRoot: File = rootProject.projectDir.parentFile
val goModuleDir: File = repoRoot.resolve("go_module")
val gomobileAar = layout.buildDirectory.file("generated/gomobile/backend.aar")
val gomobileExecutable = providers.gradleProperty("gomobileExecutable")
    .orElse(providers.environmentVariable("GOMOBILE"))
    .orElse(providers.provider {
        val userHomeExecutable = File(System.getProperty("user.home"), "go/bin/gomobile")
        if (userHomeExecutable.canExecute()) userHomeExecutable.absolutePath else "gomobile"
    })
val goCacheDir = layout.buildDirectory.dir("go-cache")
val androidSdkDir = providers.environmentVariable("ANDROID_HOME")
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

android {
    namespace = pkg
    compileSdk = 35

    defaultConfig {
        minSdk = 26

        testInstrumentationRunner = "androidx.test.runner.AndroidJUnitRunner"
        consumerProguardFiles("consumer-rules.pro")
        ndk {
            // Limit native builds to a single ABI to avoid unnecessary variants
            abiFilters += listOf("arm64-v8a")
        }
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
    lint {
        disable += "LongLogTag"
        disable += "NewApi"
    }
    compileOptions {
        sourceCompatibility = JavaVersion.VERSION_17
        targetCompatibility = JavaVersion.VERSION_17
    }
    kotlinOptions {
        jvmTarget = "17"
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
    inputs.file(goModuleDir.resolve("go.mod"))
    inputs.file(goModuleDir.resolve("go.sum"))
    outputs.file(outputFile)

    doFirst {
        outputFile.parentFile.mkdirs()
    }

    workingDir = goModuleDir
    commandLine(
        gomobileExecutable.get(),
        "bind",
        "-target=android/arm64",
        "-androidapi=26",
        "-javapkg=com.dobby.gomobile",
        "-o=${outputFile.absolutePath}",
        "go_module/kotlin_exports"
    )
    environment(
        "PATH",
        listOf(
            "/usr/local/go/bin",
            File(System.getProperty("user.home"), "go/bin").absolutePath,
            System.getenv("PATH").orEmpty()
        ).joinToString(File.pathSeparator)
    )
    environment("GO111MODULE", "on")
    environment("GOCACHE", goCacheDir.get().asFile.absolutePath)
    if (androidSdkDir.get().isNotBlank()) {
        environment("ANDROID_HOME", androidSdkDir.get())
        environment("ANDROID_SDK_ROOT", androidSdkDir.get())
    }
}

tasks.named("preBuild").configure {
    dependsOn(gomobileBindAndroid)
}

tasks.withType<KotlinCompile>().configureEach {
    dependsOn(gomobileBindAndroid)
}

tasks.withType<JavaCompile>().configureEach {
    dependsOn(gomobileBindAndroid)
}

dependencies {
    implementation(files(gomobileAar))
    implementation(libs.androidx.core.ktx)
    implementation(libs.androidx.appcompat)
    implementation(libs.material)
    testImplementation(libs.junit)
    androidTestImplementation(libs.androidx.junit)
    androidTestImplementation(libs.androidx.espresso.core)
}
