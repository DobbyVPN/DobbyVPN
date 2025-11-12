// @file:Suppress("UnstableApiUsage")

import org.gradle.api.tasks.Copy

plugins {
    id("com.android.library")
    kotlin("android")
}

val pkg = "com.dobby.outline"

android {
    namespace = pkg
    compileSdk = 35

    defaultConfig {
        minSdk = 24
        testInstrumentationRunner = "androidx.test.runner.AndroidJUnitRunner"
        consumerProguardFiles("consumer-rules.pro")

        // Указываем ABI, для которых есть готовые .so
        ndk {
            abiFilters += listOf("arm64-v8a" /*, "armeabi-v7a" если понадобится */)
        }
    }

    buildTypes {
        debug {
            // Обычные debug-настройки
        }
        release {
            isMinifyEnabled = false
            proguardFiles(
                getDefaultProguardFile("proguard-android-optimize.txt"),
                "proguard-rules.pro"
            )
        }
    }

    // Подключаем CMake один раз, путь+версия
    externalNativeBuild {
        cmake {
            path("src/main/cpp/CMakeLists.txt")
            version = "3.22.1"
        }
    }

    // Говорим Gradle, где искать готовые .so
    sourceSets {
        getByName("main") {
            jniLibs.srcDir("src/main/jniLibs")
        }
    }

    lint {
        disable += listOf("LongLogTag", "NewApi")
    }

    compileOptions {
        sourceCompatibility = JavaVersion.VERSION_17
        targetCompatibility = JavaVersion.VERSION_17
    }
    kotlinOptions {
        jvmTarget = "17"
    }
}

dependencies {
    implementation(libs.androidx.core.ktx)
    implementation(libs.androidx.appcompat)
    implementation(libs.material)

    testImplementation(libs.junit)
    androidTestImplementation(libs.androidx.junit)
    androidTestImplementation(libs.androidx.espresso.core)
}

val outputDir = rootProject.layout.projectDirectory.dir("libs")
val copyOutlineAar = tasks.register<Copy>("copyOutlineAar") {
    dependsOn("assembleDebug", "assembleRelease")

    from(layout.buildDirectory.dir("outputs/aar")) {
        include("outline-debug.aar", "outline-release.aar")
    }

    into(outputDir)
}

afterEvaluate {
    tasks.named("build").configure { finalizedBy(copyOutlineAar) }
}
