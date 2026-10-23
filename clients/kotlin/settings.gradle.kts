// The Kotlin Multiplatform client of xtandard-image (clients/kotlin, ADR 0013).
rootProject.name = "xtandard-image-kmp"

pluginManagement {
    repositories {
        mavenCentral()
        gradlePluginPortal()
    }
}

dependencyResolutionManagement {
    repositoriesMode = RepositoriesMode.FAIL_ON_PROJECT_REPOS
    repositories {
        mavenCentral()
    }
}
