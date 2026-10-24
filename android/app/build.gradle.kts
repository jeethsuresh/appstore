plugins {
    id("com.android.application")
    id("org.jetbrains.kotlin.android")
    id("org.jetbrains.kotlin.plugin.compose")
}
val releaseStore = System.getenv("STORE_KEYSTORE")
android {
    namespace = "dev.jeeth.privatestore"
    compileSdk = 36
    defaultConfig {
        applicationId = "dev.jeeth.privatestore"
        minSdk = 26
        targetSdk = 36
        versionCode = 7
        versionName = "0.4.0"
        testInstrumentationRunner = "androidx.test.runner.AndroidJUnitRunner"
    }
    compileOptions { sourceCompatibility = JavaVersion.VERSION_17; targetCompatibility = JavaVersion.VERSION_17 }
    kotlinOptions { jvmTarget = "17" }
    buildFeatures { compose = true; buildConfig = true }
    signingConfigs {
        if (releaseStore != null) create("release") {
            storeFile = file(releaseStore)
            storePassword = System.getenv("STORE_STORE_PASSWORD")
            keyAlias = System.getenv("STORE_KEY_ALIAS")
            keyPassword = System.getenv("STORE_KEY_PASSWORD")
        }
    }
    buildTypes {
        release { signingConfig = signingConfigs.findByName("release"); isMinifyEnabled = false }
        create("e2e") {
            initWith(getByName("debug"))
            applicationIdSuffix = ".testbed"
            matchingFallbacks += listOf("debug")
        }
    }
    testBuildType = providers.gradleProperty("storeTestBuildType").orElse("debug").get()
    sourceSets.getByName("e2e").res.srcDir(layout.buildDirectory.dir("generated/e2e-res"))
    lint { abortOnError = true }
}
val requireReleaseSigning = tasks.register("requireReleaseSigning") {
    doFirst { check(!releaseStore.isNullOrBlank()) { "Set STORE_KEYSTORE, STORE_STORE_PASSWORD, STORE_KEY_ALIAS and STORE_KEY_PASSWORD for release builds." } }
}
// Only the isolated test package can override its version for a real process-replacing upgrade test.
androidComponents.onVariants(androidComponents.selector().withBuildType("e2e")) { variant ->
    providers.gradleProperty("storeE2eVersionCode").orNull?.let { code ->
        variant.outputs.forEach { it.versionCode.set(code.toInt()) }
    }
}
tasks.matching { it.name == "preReleaseBuild" }.configureEach { dependsOn(requireReleaseSigning) }
dependencies {
    implementation(platform("androidx.compose:compose-bom:2025.08.00"))
    implementation("androidx.compose.material3:material3")
    implementation("androidx.compose.ui:ui")
    implementation("androidx.activity:activity-compose:1.8.2")
    implementation("androidx.work:work-runtime-ktx:2.10.1")
    testImplementation("junit:junit:4.13.2")
}
dependencies {
    androidTestImplementation("androidx.test:runner:1.7.0")
    androidTestImplementation("androidx.test.ext:junit:1.3.0")
    androidTestImplementation(platform("androidx.compose:compose-bom:2025.08.00"))
    androidTestImplementation("androidx.compose.ui:ui-test-junit4")
    androidTestImplementation("androidx.test.espresso:espresso-core:3.7.0")
}
