pluginManagement {
    repositories {
        google()
        mavenCentral()
        gradlePluginPortal()
    }
}

dependencyResolutionManagement {
    repositoriesMode.set(RepositoriesMode.FAIL_ON_PROJECT_REPOS)
    repositories {
        google()
        mavenCentral()
    }
}

// Keep the selected Gradle/AGP/Kotlin toolchain while resolving its transitive
// tools to upstream releases that pass the same High/Critical security gate.
// Register before project evaluation so plugin and legacy buildscript
// classpaths receive the policy as well as application and test graphs.
gradle.beforeProject {
    val secureDependencies = Action<DependencyResolveDetails> {
        val secureVersion = when {
            requested.group == "org.bouncycastle" -> "1.85"
            requested.group == "org.bitbucket.b_c" && requested.name == "jose4j" -> "0.9.6"
            requested.group == "org.jdom" && requested.name == "jdom2" -> "2.0.6.1"
            requested.group == "io.netty" && !requested.name.startsWith("netty-tcnative") -> "4.1.137.Final"
            requested.group == "com.google.guava" && requested.name == "guava" ->
                if (requested.version?.endsWith("-android") == true) "33.7.2-android" else "33.7.2-jre"
            else -> null
        }
        if (secureVersion != null) {
            useVersion(secureVersion)
            because("Use fixed upstream transitive dependencies in the Android tool and test graphs")
        }
    }
    buildscript.configurations.configureEach {
        resolutionStrategy.eachDependency(secureDependencies)
    }
    configurations.configureEach {
        resolutionStrategy.eachDependency(secureDependencies)
    }
}

rootProject.name = "android"
include(":app")
include(":presentation")
