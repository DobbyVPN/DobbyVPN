import io.gitlab.arturbosch.detekt.Detekt

// Top-level build file where you can add configuration options common to all sub-projects/modules.
plugins {
    alias(libs.plugins.android.application) apply false
    alias(libs.plugins.kotlin.android) apply false
    alias(libs.plugins.compose.compiler)
    alias(libs.plugins.composeMultiplatform) apply false
    alias(libs.plugins.kotlinMultiplatform) apply false
    alias(libs.plugins.android.library) apply false
    alias(libs.plugins.hydraulic.conveyor) apply false
    alias(libs.plugins.protobuf) apply false
    alias(libs.plugins.kotlin.jvm) apply false
    alias(libs.plugins.detekt)

    id("com.github.gmazzo.buildconfig") version "5.6.5" apply false
}

// Detekt covers every project declared by settings.gradle.kts. There are no
// vendored/ported modules in this build that should bypass the baseline.
allprojects {
    apply(plugin = "io.gitlab.arturbosch.detekt")

    detekt {
        buildUponDefaultConfig = true
        config.setFrom(files("${rootProject.projectDir}/detekt.yml"))
        parallel = true
        // detekt-baseline.xml and source-set-specific variants are tracked
        // with the code. With maxIssues=0 they suppress only recorded debt;
        // every new finding fails CI until it is fixed or explicitly reviewed.
    }

    tasks.withType<Detekt>().configureEach {
        val reportName = name
        reports {
            html.required.set(true)
            html.outputLocation.set(file("${project.layout.buildDirectory.get()}/reports/detekt/$reportName.html"))
            xml.required.set(true)
            xml.outputLocation.set(file("${project.layout.buildDirectory.get()}/reports/detekt/$reportName.xml"))
            sarif.required.set(true)
            sarif.outputLocation.set(file("${project.layout.buildDirectory.get()}/reports/detekt/$reportName.sarif"))
        }
    }
}
