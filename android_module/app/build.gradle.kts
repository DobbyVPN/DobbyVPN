import java.io.File
import java.util.Properties

plugins {
    id("com.android.application")
    id("org.jetbrains.kotlin.android")
    id("org.jetbrains.kotlin.plugin.compose")
}

val repoRoot = rootProject.projectDir.parentFile
val goModule = repoRoot.resolve("go_module")
fun nonBlankEnvironment(name: String) =
    providers.environmentVariable(name)
        .map(String::trim)
        .filter { it.isNotEmpty() }
fun nonBlankGradleProperty(name: String) =
    providers.gradleProperty(name)
        .map(String::trim)
        .filter { it.isNotEmpty() }
// The caller must pass the exact Go executable selected during toolchain
// preparation. Do not infer it from ambient environment: Gradle may run in a
// separate process with a different PATH.
val goBinary = nonBlankGradleProperty("dobbyGoBinary")
val expectedGoVersion = repoRoot.resolve(".go-version").readText().trim()

val validateGoToolchain by tasks.registering {
    doLast {
        val executable = File(goBinary.get())
        check(executable.isFile && executable.canExecute()) {
            "dobbyGoBinary must name an executable Go tool: ${executable.absolutePath}"
        }
        val process = ProcessBuilder(executable.absolutePath, "env", "GOVERSION")
            .directory(goModule)
            .redirectErrorStream(true)
            .apply {
                environment()["GOTOOLCHAIN"] = "local"
                environment()["GOFLAGS"] = "-trimpath -buildvcs=false"
            }
            .start()
        val observed = process.inputStream.bufferedReader().use { it.readText().trim() }
        check(process.waitFor() == 0 && observed == "go$expectedGoVersion") {
            "dobbyGoBinary GOVERSION must be go$expectedGoVersion, got ${observed.ifEmpty { "<empty>" }}"
        }
    }
}

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
val releaseVersionName: String = nonBlankGradleProperty("android.injected.version.name")
    .orElse(nonBlankGradleProperty("versionName")).get()
    ?: error("versionName is required for the Android manifest")
val releaseVersionCode: Int = nonBlankGradleProperty("android.injected.version.code")
    .orElse(nonBlankGradleProperty("versionCode")).map(String::toInt).get()
    ?: error("versionCode is required for the Android manifest")
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
        this.versionCode = releaseVersionCode
        this.versionName = releaseVersionName
        // Keep the release identity explicit in the merged manifest.  AGP's
        // injected version properties are consumed by the DSL above, but the
        // standalone F-Droid build must also expose the same values to
        // fdroidserver's APK metadata parser.
        manifestPlaceholders["dobbyVersionCode"] = releaseVersionCode.toString()
        manifestPlaceholders["dobbyVersionName"] = releaseVersionName
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
    buildFeatures {
        buildConfig = true
        compose = true
    }
}

val downloadGoModules by tasks.registering(Exec::class) {
    dependsOn(validateGoToolchain)
    doFirst {
        commandLine(goBinary.get(), "mod", "download")
    }
    workingDir(goModule)
    environment("GOTOOLCHAIN", "local")
    environment("GOFLAGS", "-trimpath -buildvcs=false")
}

val buildGoBackend by tasks.registering {
    val ndkHome = nonBlankEnvironment("ANDROID_NDK_HOME")
        .orElse(nonBlankEnvironment("ANDROID_NDK_ROOT"))
        .orElse(androidSdkRoot.map { File(it, "ndk/28.1.13356709").absolutePath })
        .orElse("")
    val api = providers.gradleProperty("android.ndk.api").orElse("26")
    val abis = mapOf(
        "arm64-v8a" to ("arm64" to "aarch64-linux-android"),
        "x86_64" to ("amd64" to "x86_64-linux-android"),
    )
    val outputFiles = abis.map { (androidAbi, _) ->
        layout.buildDirectory.file("generated/go-libs/$androidAbi/libdobby_vpn.so").get().asFile
    }
    val goBuildRoot = layout.buildDirectory.dir("generated/go-build")
    inputs.files(fileTree(goModule) { include("**/*.go", "go.mod", "go.sum") })
    outputs.files(outputFiles)
    dependsOn(validateGoToolchain, downloadGoModules)
    doLast {
        check(ndkHome.get().isNotBlank()) {
            "ANDROID_NDK_HOME (or ANDROID_NDK_ROOT) is required to build the shared Go Android backend"
        }
        val ndk = File(ndkHome.get())
        val toolchain = ndk.resolve("toolchains/llvm/prebuilt")
            .listFiles()?.singleOrNull()
            ?: error("Android NDK LLVM toolchain is unavailable under $ndk")
        val apiLevel = api.get()
        // Keep Go/cgo's process-visible inputs identical for the hosted
        // Android builder and the F-Droid buildserver. In particular, do
        // not let each builder's HOME, GOENV, temporary directory, or
        // inherited CGO flags enter the native shared object.
        val reproducibleBuildRoot = goBuildRoot.get().asFile
        val goCache = reproducibleBuildRoot.resolve("cache")
        val goTemp = reproducibleBuildRoot.resolve("tmp")
        goCache.mkdirs()
        goTemp.mkdirs()
        abis.forEach { (androidAbi, pair) ->
            val (goArch, triple) = pair
            val output = layout.buildDirectory.dir("generated/go-libs/$androidAbi").get().asFile
                .resolve("libdobby_vpn.so")
            output.parentFile.mkdirs()
            val compiler = toolchain.resolve("bin/${triple}${apiLevel}-clang")
            check(compiler.isFile) { "Android NDK compiler is unavailable: $compiler" }
            val command = listOf(
                goBinary.get(), "build", "-buildmode=c-shared", "-tags=android,accessibility,static",
                "-trimpath", "-ldflags=-buildid= -s -w", "-o", output.absolutePath, "./cmd/dobbyandroid"
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
                environment("GO111MODULE", "on")
                environment("GOENV", "off")
                environment("GOTOOLCHAIN", "local")
                environment("GOFLAGS", "-trimpath -buildvcs=false")
                environment("GOCACHE", goCache.absolutePath)
                environment("GOTMPDIR", goTemp.absolutePath)
                // Gradle inherits the caller's environment. Clear every
                // conventional C flag family so a buildserver image cannot
                // silently alter cgo's wrapper compilation or final link.
                environment("CGO_CFLAGS", "")
                environment("CGO_CPPFLAGS", "")
                environment("CGO_CXXFLAGS", "")
                environment("CGO_LDFLAGS", "")
                environment("CFLAGS", "")
                environment("CPPFLAGS", "")
                environment("CXXFLAGS", "")
                environment("LDFLAGS", "")
                environment("LANG", "C")
                environment("LC_ALL", "C")
                environment("TZ", "UTC")
                environment("SOURCE_DATE_EPOCH", "0")
            }
        }
    }
}

tasks.named("preBuild") { dependsOn(buildGoBackend) }

// F-Droid's Gradle output discovery looks below the selected Gradle root
// (android_module/build), while AGP normally writes this app to app/build.
// Keep the normal app output for the Android driver and mirror the unsigned
// release APK at the root only when that task completes.
val mirrorFroidReleaseApk by tasks.registering(Copy::class) {
    from(layout.buildDirectory.file("outputs/apk/release/app-release-unsigned.apk"))
    into(rootProject.projectDir.resolve("build/outputs/apk/release"))
}

tasks.matching { it.name == "assembleRelease" }.configureEach {
    finalizedBy(mirrorFroidReleaseApk)
}

dependencies {
    implementation("androidx.core:core-ktx:1.15.0")
    implementation("androidx.activity:activity-compose:1.10.1")
    implementation(platform("androidx.compose:compose-bom:2025.12.00"))
    implementation("androidx.compose.material3:material3")
    implementation("androidx.compose.ui:ui")
    implementation("androidx.compose.ui:ui-tooling-preview")
    debugImplementation("androidx.compose.ui:ui-tooling")
    androidTestImplementation("androidx.test:runner:1.6.2")
    androidTestImplementation("androidx.test.ext:junit:1.2.1")
    androidTestImplementation("androidx.test.uiautomator:uiautomator:2.4.0")
    testImplementation("junit:junit:4.13.2")
}
