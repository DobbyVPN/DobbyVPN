import org.jetbrains.kotlin.gradle.ExperimentalKotlinGradlePluginApi
import org.jetbrains.kotlin.gradle.dsl.JvmTarget

plugins {
    alias(libs.plugins.android.application)
    alias(libs.plugins.composeMultiplatform)
    alias(libs.plugins.compose.compiler)
    alias(libs.plugins.jetbrains.kotlin.serialization)
    alias(libs.plugins.kotlinMultiplatform)
    alias(libs.plugins.hydraulic.conveyor)
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

    iosArm64().binaries.framework {
        baseName = "app"
        isStatic = true
    }

    sourceSets {

        androidMain.dependencies {
            implementation(compose.preview)
            implementation(libs.androidx.activity.compose)
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

            implementation(files("../libs/go_client_android.aar"))

            implementation(libs.okhttp)
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
        }

        jvmMain.dependencies {
            implementation(compose.desktop.currentOs)
            implementation(libs.skiko.win)
            implementation(libs.skiko.mac.amd64)
            implementation(libs.skiko.mac.arm64)
            implementation(libs.skiko.linux)

            implementation(libs.kotlinx.coroutines.swing)
            implementation(libs.jna)
            implementation(libs.gson)
        }
    }
}

compose.desktop {
    application {
        mainClass = "MainKt"
    }
}


android {
    namespace = "com.example.ck_client"
    compileSdk = 35

    defaultConfig {
        applicationId = "com.example.ck_client"
        minSdk = 26
        targetSdk = 35
        versionCode = 1
        versionName = "1.0"

        vectorDrawables {
            useSupportLibrary = true
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

dependencies {
    implementation(project(":awg"))
    debugImplementation(libs.androidx.ui.tooling)
    debugImplementation(libs.androidx.ui.test.manifest)
}
