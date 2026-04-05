// Top-level build file where you can add configuration options common to all sub-projects/modules.
plugins {
    alias(libs.plugins.android.application) apply false
    alias(libs.plugins.kotlin.android) apply false
    alias(libs.plugins.compose.compiler)
    alias(libs.plugins.composeMultiplatform) apply false
    alias(libs.plugins.kotlinMultiplatform) apply false
    alias(libs.plugins.android.library) apply false
    alias(libs.plugins.hydraulic.conveyor) apply false
    alias(libs.plugins.detekt)

    id("com.github.gmazzo.buildconfig") version "5.6.5" apply false
}

// detekt for all subprojects except vendored/ported modules
val detektExcluded = setOf("outline", "awg")
allprojects {
    if (project.name !in detektExcluded) {
        apply(plugin = "io.gitlab.arturbosch.detekt")

        detekt {
            buildUponDefaultConfig = true
            config.setFrom(files("${rootProject.projectDir}/detekt.yml"))
            parallel = true
            reports {
                html.required.set(true)
                html.outputLocation.set(file("${project.layout.buildDirectory.get()}/reports/detekt/detekt.html"))
                xml.required.set(true)
                xml.outputLocation.set(file("${project.layout.buildDirectory.get()}/reports/detekt/detekt.xml"))
                sarif.required.set(true)
                sarif.outputLocation.set(file("${project.layout.buildDirectory.get()}/reports/detekt/detekt.sarif"))
            }
        }
    }
}
