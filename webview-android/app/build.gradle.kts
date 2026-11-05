plugins { id("com.android.application") }
val releaseStore = System.getenv("STORE_KEYSTORE")
android {
    namespace = "cool.gravitas.webview"
    compileSdk = 36
    defaultConfig {
        minSdk = 26
        targetSdk = 36
        versionCode = 1
        versionName = "0.1.0"
        testInstrumentationRunner = "androidx.test.runner.AndroidJUnitRunner"
    }
    flavorDimensions += "site"
    productFlavors {
        create("work") {
            dimension = "site"
            applicationId = "cool.gravitas.work"
            resValue("string", "app_name", "Work")
            buildConfigField("String", "APP_ORIGIN", "\"https://work.gravitas.cool\"")
            manifestPlaceholders["launcherIcon"] = "@drawable/ic_work"
        }
        create("news") {
            dimension = "site"
            applicationId = "cool.gravitas.news"
            resValue("string", "app_name", "News")
            buildConfigField("String", "APP_ORIGIN", "\"https://news.gravitas.cool\"")
            manifestPlaceholders["launcherIcon"] = "@drawable/ic_news"
        }
    }
    compileOptions { sourceCompatibility = JavaVersion.VERSION_17; targetCompatibility = JavaVersion.VERSION_17 }
    buildFeatures { buildConfig = true }
    signingConfigs {
        if (releaseStore != null) create("release") {
            storeFile = file(releaseStore)
            storePassword = System.getenv("STORE_STORE_PASSWORD")
            keyAlias = System.getenv("STORE_KEY_ALIAS")
            keyPassword = System.getenv("STORE_KEY_PASSWORD")
        }
    }
    buildTypes { release { signingConfig = signingConfigs.findByName("release") } }
    lint { abortOnError = true }
}
tasks.matching { it.name.matches(Regex("pre(Work|News)ReleaseBuild")) }.configureEach {
    doFirst { check(!releaseStore.isNullOrBlank()) { "Release builds require the permanent Store signing key." } }
}
dependencies {
    testImplementation("junit:junit:4.13.2")
    androidTestImplementation("androidx.test:runner:1.7.0")
    androidTestImplementation("androidx.test.ext:junit:1.3.0")
    androidTestImplementation("androidx.test:rules:1.7.0")
    androidTestImplementation("androidx.test.uiautomator:uiautomator:2.3.0")
}
