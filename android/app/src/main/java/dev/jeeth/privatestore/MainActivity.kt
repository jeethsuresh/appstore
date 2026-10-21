package dev.jeeth.privatestore

import android.Manifest
import android.content.Intent
import android.net.Uri
import android.os.Build
import android.os.Bundle
import android.provider.Settings
import androidx.activity.ComponentActivity
import androidx.activity.compose.rememberLauncherForActivityResult
import androidx.activity.compose.setContent
import androidx.activity.result.contract.ActivityResultContracts
import androidx.compose.foundation.layout.*
import androidx.compose.foundation.rememberScrollState
import androidx.compose.foundation.verticalScroll
import androidx.compose.material3.*
import androidx.compose.runtime.*
import androidx.compose.ui.Modifier
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.text.input.PasswordVisualTransformation
import androidx.compose.ui.unit.dp
import androidx.lifecycle.Lifecycle
import androidx.lifecycle.LifecycleEventObserver
import android.content.SharedPreferences
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.launch
import kotlinx.coroutines.withContext
import java.text.DateFormat
import java.util.Date

class MainActivity : ComponentActivity() {
    companion object { @Volatile var visible = false }
    override fun onStart() { super.onStart(); visible = true }
    override fun onStop() { visible = false; super.onStop() }
    override fun onCreate(savedInstanceState: Bundle?) {
        super.onCreate(savedInstanceState)
        setContent { MaterialTheme(colorScheme = lightColorScheme(primary = Color(0xFF47633F), background = Color(0xFFF8FAF4))) { Surface(Modifier.fillMaxSize()) { StoreScreen() } } }
    }
    @OptIn(ExperimentalLayoutApi::class)
    @Composable private fun StoreScreen() {
        val vault = remember { SessionVault(this) }
        var message by remember { mutableStateOf("") }
        var session by remember { mutableStateOf(runCatching { vault.load() }.getOrElse { vault.clear(); null }) }
        var apps by remember { mutableStateOf<List<StoreApp>>(emptyList()) }
        var busy by remember { mutableStateOf(false) }
        var downloadFraction by remember { mutableFloatStateOf(0f) }
        var revision by remember { mutableIntStateOf(0) }
        var server by remember { mutableStateOf("") }
        var invite by remember { mutableStateOf("") }
        var name by remember { mutableStateOf(Build.MODEL) }
        var selected by remember { mutableStateOf<StoreApp?>(null) }
        val prefs = remember { getSharedPreferences("updates", MODE_PRIVATE) }
        var auto by remember { mutableStateOf(prefs.getBoolean("auto", true)) }
        var installAllowed by remember { mutableStateOf(packageManager.canRequestPackageInstalls()) }
        val scope = rememberCoroutineScope()
        val notifications = rememberLauncherForActivityResult(ActivityResultContracts.RequestPermission()) { allowed -> revision++; if (!allowed) message = "Enable notifications in Android settings to receive update confirmations." }
        val permission = rememberLauncherForActivityResult(ActivityResultContracts.StartActivityForResult()) { installAllowed = packageManager.canRequestPackageInstalls(); message = if (installAllowed) "App installation enabled. Tap Install again." else "Enable app installation to continue." }
        fun refresh() { val current = session ?: return; busy = true; scope.launch { try { apps = withContext(Dispatchers.IO) { StoreApi(current).apps() }; message = prefs.getString("status", "") ?: "" } catch (e: Exception) { message = e.message ?: "Could not refresh" } finally { busy = false } } }
        DisposableEffect(session) {
            val observer = LifecycleEventObserver { _, event ->
                if (event == Lifecycle.Event.ON_RESUME) { installAllowed = packageManager.canRequestPackageInstalls(); revision++; if (session != null && !busy) refresh() }
            }
            lifecycle.addObserver(observer)
            val listener = SharedPreferences.OnSharedPreferenceChangeListener { _, key ->
                scope.launch { revision++; if (key == "status") message = prefs.getString("status", "") ?: "" }
            }
            prefs.registerOnSharedPreferenceChangeListener(listener)
            onDispose { lifecycle.removeObserver(observer); prefs.unregisterOnSharedPreferenceChangeListener(listener) }
        }
        LaunchedEffect(session) { if (session != null) { UpdateWorker.schedule(this@MainActivity); refresh() } }
        Column(Modifier.fillMaxSize().verticalScroll(rememberScrollState()).statusBarsPadding().navigationBarsPadding().padding(24.dp), verticalArrangement = Arrangement.spacedBy(16.dp)) {
            Text("Private Store", style = MaterialTheme.typography.headlineLarge)
            Text(if (session == null) "Your apps, delivered privately." else "A small home for your apps.", style = MaterialTheme.typography.bodyLarge)
            if (session == null) {
                Text("Enter the HTTPS address of your store and a device invitation from its administrator.")
                OutlinedTextField(server, { server = it }, label = { Text("Store address") }, singleLine = true, modifier = Modifier.fillMaxWidth())
                OutlinedTextField(invite, { invite = it }, label = { Text("Invitation") }, visualTransformation = PasswordVisualTransformation(), singleLine = true, modifier = Modifier.fillMaxWidth())
                OutlinedTextField(name, { name = it }, label = { Text("Device name") }, singleLine = true, modifier = Modifier.fillMaxWidth())
                Button(enabled = !busy && invite.isNotBlank() && name.isNotBlank(), onClick = {
                    busy = true; scope.launch { try {
                        val result = withContext(Dispatchers.IO) { StoreApi(Session(Versions.baseUrl(server), "")).enroll(invite, name) }
                        vault.save(result); session = result; invite = ""; message = "Device enrolled"
                        if (Build.VERSION.SDK_INT >= 33) notifications.launch(Manifest.permission.POST_NOTIFICATIONS)
                    } catch (e: Exception) { message = e.message ?: "Enrollment failed" } finally { busy = false } }
                }) { Text("Enroll this device") }
            } else {
                Text(session!!.baseUrl, style = MaterialTheme.typography.bodySmall)
                FlowRow(horizontalArrangement = Arrangement.spacedBy(12.dp), verticalArrangement = Arrangement.spacedBy(8.dp)) {
                    Button(enabled = !busy, onClick = { refresh() }) { Text("Check for updates") }
                    TextButton(enabled = !busy, onClick = { if (Build.VERSION.SDK_INT >= 33 && !Notices.enabled(this@MainActivity)) notifications.launch(Manifest.permission.POST_NOTIFICATIONS) else startActivity(Intent(Settings.ACTION_APP_NOTIFICATION_SETTINGS).putExtra(Settings.EXTRA_APP_PACKAGE, packageName)) }) { Text("Notifications") }
                }
                if (!installAllowed) OutlinedButton(onClick = { permission.launch(Intent(Settings.ACTION_MANAGE_UNKNOWN_APP_SOURCES, Uri.parse("package:$packageName"))) }) { Text("Allow app installation") }
                Row {
                    Switch(checked = auto, onCheckedChange = { auto = it; prefs.edit().putBoolean("auto", it).apply() })
                    Text("Automatic updates", modifier = Modifier.padding(start = 12.dp, top = 12.dp))
                }
                Text("Checks run about every 6 hours when connected and the battery is not low. Android may ask you to approve an update. Notifications must be enabled for background installation.", style = MaterialTheme.typography.bodySmall)
                val checked = prefs.getLong("lastCheck", 0)
                if (checked > 0) Text("Last background check: ${DateFormat.getDateTimeInstance().format(Date(checked))}", style = MaterialTheme.typography.bodySmall)
                apps.forEach { app ->
                    val r = app.release; val installed = remember(app.packageName, revision) { Installer.installed(this@MainActivity, app.packageName) }
                    val update = r != null && Versions.newer(installed, r.versionCode)
                    Card(Modifier.fillMaxWidth()) { Column(Modifier.padding(20.dp), verticalArrangement = Arrangement.spacedBy(10.dp)) {
                        Text(app.name, style = MaterialTheme.typography.titleLarge)
                        Text(app.description)
                        Text(when { r == null -> "No verified release available"; installed == null -> "Version ${r.versionName} · ${(r.size / 1048576.0).let { "%.1f".format(it) }} MB"; update -> "Update to ${r.versionName}"; installed > r.versionCode -> "Installed a newer version · code $installed"; else -> "Up to date · ${r.versionName}" }, style = MaterialTheme.typography.labelLarge)
                        if (r != null && r.notes.isNotBlank()) TextButton(onClick = { selected = app }) { Text("Release notes") }
                        Button(enabled = !busy && update && r!!.minSdk <= Build.VERSION.SDK_INT, onClick = {
                            if (!packageManager.canRequestPackageInstalls()) { permission.launch(Intent(Settings.ACTION_MANAGE_UNKNOWN_APP_SOURCES, Uri.parse("package:$packageName"))) }
                            else { val current = session!!; busy = true; downloadFraction = 0f; message = "Downloading ${app.name}…"; scope.launch { try {
                                withContext(Dispatchers.IO) { Installer.install(this@MainActivity, StoreApi(current).download(this@MainActivity, app) { done,total -> scope.launch { downloadFraction = done.toFloat()/total.toFloat() } }, app, background = false) }
                                message = "Follow Android's installation prompt, then check for updates."
                            } catch (e: Exception) { message = e.message ?: "Installation failed" } finally { busy = false } } }
                        }) { Text(if (installed == null) "Install" else if (update) "Update" else "Installed") }
                    } }
                }
                TextButton(enabled = !busy, onClick = { busy = true; scope.launch { try {
                    withContext(Dispatchers.IO) { StoreApi(session!!).revoke() }
                    vault.clear(); UpdateWorker.cancel(this@MainActivity); session = null; apps = emptyList(); message = "Device access revoked"
                } catch (e: Exception) { message = "Could not revoke access: ${e.message}" } finally { busy = false } } }) { Text("Revoke this device and sign out") }
                TextButton(enabled = !busy, onClick = { vault.clear(); UpdateWorker.cancel(this@MainActivity); session = null; apps = emptyList(); message = "Signed out locally. Ask the administrator to revoke old device access." }) { Text("Forget local enrollment") }
            }
            if (busy) { if (downloadFraction > 0f) LinearProgressIndicator(progress = { downloadFraction }, modifier = Modifier.fillMaxWidth()) else LinearProgressIndicator(Modifier.fillMaxWidth()) }
            if (message.isNotBlank()) Text(message, color = MaterialTheme.colorScheme.primary)
        }
        selected?.let { app -> AlertDialog(onDismissRequest = { selected = null }, title = { Text("${app.name} · ${app.release?.versionName}") }, text = { Text(app.release?.notes ?: "", modifier = Modifier.verticalScroll(rememberScrollState())) }, confirmButton = { TextButton(onClick = { selected = null }) { Text("Done") } }) }
    }
}
