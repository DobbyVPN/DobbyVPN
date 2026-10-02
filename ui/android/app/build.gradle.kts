import java.io.File
import java.util.Properties
import org.jetbrains.kotlin.gradle.dsl.JvmTarget

plugins {
    id("com.android.application")
    id("org.jetbrains.kotlin.android")
    id("org.jetbrains.kotlin.plugin.compose")
}

val repoRoot = rootProject.projectDir.parentFile.parentFile
val goModule = repoRoot.resolve("core")
val distributionLicenseAssets = layout.buildDirectory.dir("generated/distribution-license-assets")
val pinnedAndroidNdkVersion = "28.1.13356709"
fun nonBlankEnvironment(name: String) =
    providers.environmentVariable(name)
        .map(String::trim)
        .filter { it.isNotEmpty() }
fun nonBlankGradleProperty(name: String) =
    providers.gradleProperty(name)
        .map(String::trim)
        .filter { it.isNotEmpty() }
val generatedGradleLockFile = nonBlankGradleProperty("dobbyGradleLockFile")
    .map { project.file(it) }
// The caller must pass the exact Go executable selected during toolchain
// preparation. Do not infer it from ambient environment: Gradle may run in a
// separate process with a different PATH.
val goBinary = nonBlankGradleProperty("dobbyGoBinary")
val expectedGoVersion = repoRoot.resolve(".go-version").readText().trim()

dependencyLocking {
    generatedGradleLockFile.orNull?.let {
        lockAllConfigurations()
        lockFile = it
    }
}

val resolveAndroidSecurityDependencies by tasks.registering {
    doLast {
        check(generatedGradleLockFile.isPresent) {
            "dobbyGradleLockFile is required to resolve Android security dependencies"
        }
        configurations.getByName("releaseRuntimeClasspath").resolve()
    }
}

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
val ndkHome = nonBlankEnvironment("ANDROID_NDK_HOME")
    .orElse(nonBlankEnvironment("ANDROID_NDK_ROOT"))
    .orElse(androidSdkRoot.map { File(it, "ndk/$pinnedAndroidNdkVersion").absolutePath })
    .orElse("")
val releaseVersionName: String = nonBlankGradleProperty("android.injected.version.name")
    .orElse(nonBlankGradleProperty("versionName")).get()
    ?: error("versionName is required for the Android manifest")
val releaseVersionCode: Int = nonBlankGradleProperty("android.injected.version.code")
    .orElse(nonBlankGradleProperty("versionCode")).map(String::toInt).get()
    ?: error("versionCode is required for the Android manifest")
val sourceCommit = providers.gradleProperty("projectRepositoryCommit").getOrElse("N/A")
val copyLicenseAssets by tasks.registering(Copy::class) {
    from(repoRoot) {
        include("LICENSE", "THIRD_PARTY_NOTICES", "LICENSES/**")
        into("licenses")
    }
    into(distributionLicenseAssets)
}

android {
    namespace = "com.dobby.vpn"
    compileSdk = 35
    ndkVersion = pinnedAndroidNdkVersion
    ndkPath = ndkHome.get()
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

    packaging {
        jniLibs {
            useLegacyPackaging = true
        }
    }

    compileOptions {
        sourceCompatibility = JavaVersion.VERSION_17
        targetCompatibility = JavaVersion.VERSION_17
    }
    sourceSets["main"].jniLibs.srcDir(layout.buildDirectory.dir("generated/go-libs"))
    sourceSets["main"].assets.srcDir(distributionLicenseAssets)
    buildFeatures {
        buildConfig = true
        compose = true
    }
}

kotlin {
    compilerOptions {
        jvmTarget = JvmTarget.fromTarget("17")
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

val api = providers.gradleProperty("android.ndk.api").orElse("26")
val abis = mapOf(
    "arm64-v8a" to ("arm64" to "aarch64-linux-android"),
    "x86_64" to ("amd64" to "x86_64-linux-android"),
)
val backendInputs = fileTree(goModule) { include("**/*.go", "go.mod", "go.sum") }
val goBuildRoot = layout.buildDirectory.dir("generated/go-build")
val backendTasks = abis.map { (androidAbi, pair) ->
    val (goArch, triple) = pair
    tasks.register<Exec>("buildGoBackend_${androidAbi.replace('-', '_')}") {
        dependsOn(validateGoToolchain, downloadGoModules)
        inputs.files(backendInputs)
        outputs.file(layout.buildDirectory.file("generated/go-libs/$androidAbi/libdobby_vpn.so"))
        doFirst {
            check(ndkHome.get().isNotBlank()) {
                "ANDROID_NDK_HOME (or ANDROID_NDK_ROOT) is required to build the shared Go Android backend"
            }
            val ndk = File(ndkHome.get())
            val observedNdkRevision = ndk.resolve("source.properties")
                .takeIf { it.isFile }
                ?.readLines()
                ?.firstOrNull { it.trimStart().startsWith("Pkg.Revision") }
                ?.substringAfter("=")
                ?.trim()
            check(observedNdkRevision == pinnedAndroidNdkVersion) {
                "Android NDK $pinnedAndroidNdkVersion is required, found ${observedNdkRevision ?: "<missing>"} at $ndk"
            }
            val toolchain = ndk.resolve("toolchains/llvm/prebuilt")
                .listFiles()?.singleOrNull()
                ?: error("Android NDK LLVM toolchain is unavailable under $ndk")
            val apiLevel = api.get()
            val output = layout.buildDirectory.file("generated/go-libs/$androidAbi/libdobby_vpn.so")
                .get().asFile
            output.parentFile.mkdirs()
            val compiler = toolchain.resolve("bin/${triple}${apiLevel}-clang")
            val linker = toolchain.resolve("bin/${triple}${apiLevel}-clang++")
            check(compiler.isFile) { "Android NDK compiler is unavailable: $compiler" }
            check(linker.isFile) { "Android NDK C++ linker is unavailable: $linker" }

            // Keep Go/cgo's process-visible inputs identical across the two
            // reproducibility builds. In particular, do not let HOME, GOENV,
            // temporary directories, or inherited CGO flags affect the output.
            val reproducibleBuildRoot = goBuildRoot.get().asFile
            val goCache = reproducibleBuildRoot.resolve("cache")
            val goTemp = reproducibleBuildRoot.resolve("tmp")
            goCache.mkdirs()
            goTemp.mkdirs()
            commandLine(
                goBinary.get(), "build", "-buildmode=c-shared", "-tags=android,accessibility,static",
                "-trimpath",
                // The bridge arrives as a static archive, so Go does not
                // infer a C++ external linker from source files. Select the
                // NDK C++ driver explicitly so -static-libstdc++ takes effect.
                "-ldflags=-buildid= -s -w -extld=${linker.absolutePath} -extldflags=-static-libstdc++",
                "-o", output.absolutePath, "./cmd/dobbyandroid"
            )
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
// The ABI builds share the Go cache and must run one at a time, including
// when Gradle is invoked with parallel execution enabled.
backendTasks.zipWithNext().forEach { (previous, current) ->
    current.configure { mustRunAfter(previous) }
}
val buildGoBackend by tasks.registering {
    dependsOn(backendTasks)
}

tasks.named("preBuild") { dependsOn(buildGoBackend, copyLicenseAssets) }


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
    androidTestImplementation("androidx.test.uiautomator:uiautomator:2.3.0")
    testImplementation("junit:junit:4.13.2")
}
