package dev.jeeth.privatestore

import androidx.compose.ui.Alignment
import androidx.compose.ui.semantics.semantics
import androidx.compose.ui.semantics.contentDescription

import android.Manifest
import android.content.ActivityNotFoundException
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
    private var loginCallback by mutableStateOf<String?>(null)
    private fun acceptCallback(value: Intent) {
        if (value.action == Intent.ACTION_VIEW) loginCallback = value.dataString
        value.data = null
    }
    override fun onNewIntent(intent: Intent) { super.onNewIntent(intent); setIntent(intent); acceptCallback(intent) }
    override fun onStart() { super.onStart(); visible = true }
    override fun onStop() { visible = false; super.onStop() }
    override fun onCreate(savedInstanceState: Bundle?) {
        super.onCreate(savedInstanceState)
        SelfUpdateRecovery.reconcile(this)
        acceptCallback(intent)
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
        var server by remember { mutableStateOf(GravitasLogin.STORE) }
        var advanced by remember { mutableStateOf(false) }
        var pending by remember { mutableStateOf(runCatching { vault.pending() }.getOrElse { vault.clearPending(); null }) }
        var invite by remember { mutableStateOf("") }
        var username by remember { mutableStateOf("") }
        var defaultDomain by remember { mutableStateOf("") }
        var defaultIssuer by remember { mutableStateOf("") }
        var name by remember { mutableStateOf(Build.MODEL) }
        var selected by remember { mutableStateOf<StoreApp?>(null) }
        val prefs = remember { getSharedPreferences("updates", MODE_PRIVATE) }
        var auto by remember { mutableStateOf(prefs.getBoolean("auto", true)) }
        var appNotifications by remember { mutableStateOf(prefs.getBoolean("appNotifications", true)) }
        var batteryExempt by remember { mutableStateOf(NotificationRelay.ignoresBatteryOptimizations(this)) }
        var installAllowed by remember { mutableStateOf(packageManager.canRequestPackageInstalls()) }
        val scope = rememberCoroutineScope()
        val notifications = rememberLauncherForActivityResult(ActivityResultContracts.RequestPermission()) { allowed -> revision++; if (!allowed) message = "Enable notifications in Android settings to receive update confirmations." }
        val permission = rememberLauncherForActivityResult(ActivityResultContracts.StartActivityForResult()) { installAllowed = packageManager.canRequestPackageInstalls(); message = if (installAllowed) "App installation enabled. Tap Install again." else "Enable app installation to continue." }
        fun refresh() { val current = session ?: return; revision++; busy = true; scope.launch { try { val received = withContext(Dispatchers.IO) { StoreApi(current).apps() }; if (session?.token != current.token) return@launch; apps = received; NotificationRelay.configure(this@MainActivity, current, apps, appNotifications); message = prefs.getString("status", "") ?: "" } catch (e: Exception) { message = e.message ?: "Could not refresh"; if (e is ApiException && e.code == 401 && withContext(Dispatchers.IO) { BrokerManager.engine(this@MainActivity).invalidateDevice(current) }) { session = null; apps = emptyList(); UpdateWorker.cancel(this@MainActivity) } } finally { busy = false } } }
        fun openLogin(address: String) { startActivity(Intent(Intent.ACTION_VIEW, Uri.parse(address)).addCategory(Intent.CATEGORY_BROWSABLE)) }
        fun signIn(register: Boolean = false) {
            val current = session ?: Session(GravitasLogin.STORE, "")
            if (current.baseUrl != GravitasLogin.STORE) { message = "Gravitas sign-in is available at ${GravitasLogin.STORE}. Use an invitation for this other store."; return }
            busy = true; message = "Opening Gravitas sign-in…"
            scope.launch { try {
                val requestedIdentity = if (register) "" to defaultIssuer else GravitasLogin.identity(username.ifBlank { current.account }, defaultDomain)
                val identity = if (requestedIdentity.second == "https://$defaultDomain") requestedIdentity.first to defaultIssuer else requestedIdentity
                val request = withContext(Dispatchers.IO) { StoreApi(current).startLogin(GravitasLogin.create(current.baseUrl, name, broker = identity.second == "https://gravitas.cool", username = identity.first, issuer = identity.second), register) }
                vault.savePending(request); pending = request
                openLogin(request.authorizationUrl)
                message = "Finish signing in in your browser, then return to Private Store."
            } catch (e: Exception) { vault.clearPending(); pending = null; message = e.message ?: "Could not open sign-in" } finally { busy = false } }
        }
        LaunchedEffect(Unit) {
            try { val config = withContext(Dispatchers.IO) { StoreApi(Session(GravitasLogin.STORE, "")).authConfig() }; defaultDomain = config.getString("defaultDomain"); defaultIssuer = config.getString("issuer") }
            catch (_: Exception) { message = "Connect to the store to sign in." }
        }
        val callback = loginCallback
        LaunchedEffect(callback) {
            val returned = callback ?: return@LaunchedEffect
            val request = runCatching { vault.pending() }.getOrNull()
            if (request == null) { if (session == null) message = "This sign-in has expired or was cancelled. Start again."; pending = null; loginCallback = null; return@LaunchedEffect }
            val code = try { GravitasLogin.code(request, returned) } catch (e: Exception) {
                message = e.message ?: "Unexpected sign-in link"; loginCallback = null; return@LaunchedEffect
            }
            busy = true
            try {
                val current = session?.takeIf { it.baseUrl == request.baseUrl } ?: Session(request.baseUrl, "")
                val result = withContext(Dispatchers.IO) { StoreApi(current).finishLogin(request, code) }
                if(result.broker!=null) withContext(Dispatchers.IO) { BrokerManager.engine(this@MainActivity).accept(result.broker,result.session) } else vault.save(result.session)
                if(vault.load()?.token!=result.session.token) throw BrokerFailure(BrokerError.CANCELLED)
                vault.clearPending();pending=null;session=result.session;message="Signed in with Gravitas"
                if (Build.VERSION.SDK_INT >= 33) notifications.launch(Manifest.permission.POST_NOTIFICATIONS)
            } catch (e: Exception) {
                vault.clearPending(); pending = null; message = e.message ?: "Sign-in failed. Start again."
            } finally { busy = false; loginCallback = null }
        }
        DisposableEffect(session) {
            val observer = LifecycleEventObserver { _, event ->
                if (event == Lifecycle.Event.ON_RESUME) {
                    runCatching { BrokerAccounts.reconcile(this@MainActivity) }
                    installAllowed = packageManager.canRequestPackageInstalls()
                    batteryExempt = NotificationRelay.ignoresBatteryOptimizations(this@MainActivity)
                    // Recheck power policy immediately, even if catalog refresh
                    // is busy or fails after returning from Android settings.
                    if (session != null && appNotifications) NotificationRelay.start(this@MainActivity)
                    revision++
                    if (session != null && !busy) refresh()
                }
            }
            lifecycle.addObserver(observer)
            val listener = SharedPreferences.OnSharedPreferenceChangeListener { _, key ->
                scope.launch { revision++; if (key == "status") message = prefs.getString("status", "") ?: "" }
            }
            prefs.registerOnSharedPreferenceChangeListener(listener)
            val sessionPrefs=getSharedPreferences("session",MODE_PRIVATE)
            val sessionListener=SharedPreferences.OnSharedPreferenceChangeListener { _,_ -> scope.launch { val value=runCatching { vault.load() }.getOrNull();if(value==null && session!=null) { session=null;apps=emptyList();message="Sign in with Gravitas again" } } }
            sessionPrefs.registerOnSharedPreferenceChangeListener(sessionListener)
            onDispose { lifecycle.removeObserver(observer); prefs.unregisterOnSharedPreferenceChangeListener(listener);sessionPrefs.unregisterOnSharedPreferenceChangeListener(sessionListener) }
        }
        LaunchedEffect(session) { if (session != null) { UpdateWorker.schedule(this@MainActivity); refresh() } }
        Column(Modifier.fillMaxSize().verticalScroll(rememberScrollState()).statusBarsPadding().navigationBarsPadding().padding(24.dp), verticalArrangement = Arrangement.spacedBy(16.dp)) {
            Text("Private Store", style = MaterialTheme.typography.headlineLarge)
            Text(if (session == null) "Your apps, delivered privately." else "A small home for your apps.", style = MaterialTheme.typography.bodyLarge)
            if (session == null) {
                Text("Sign in with your Gravitas account to browse, install and update your apps. Your browser handles sign-in; Private Store never asks for your password.")
                OutlinedTextField(name, { name = it }, label = { Text("Device name") }, singleLine = true, modifier = Modifier.fillMaxWidth())
                Row(horizontalArrangement = Arrangement.spacedBy(8.dp), verticalAlignment = Alignment.CenterVertically) {
                    OutlinedTextField(username, { username = it }, label = { Text("Username") }, placeholder = { Text("username@$defaultDomain") }, singleLine = true, modifier = Modifier.weight(1f), keyboardOptions = androidx.compose.foundation.text.KeyboardOptions(capitalization = androidx.compose.ui.text.input.KeyboardCapitalization.None, autoCorrectEnabled = false, imeAction = androidx.compose.ui.text.input.ImeAction.Go), keyboardActions = androidx.compose.foundation.text.KeyboardActions(onGo = { if (!busy && defaultDomain.isNotEmpty() && username.isNotBlank() && name.isNotBlank()) signIn() }))
                    Button(enabled = !busy && defaultDomain.isNotEmpty() && username.isNotBlank() && name.isNotBlank(), onClick = { signIn() }, modifier = Modifier.semantics { contentDescription = "Sign in" }) { Text("🔥") }
                }
                Text("Without a domain, sign in at $defaultDomain.")
                TextButton(enabled = !busy && defaultDomain.isNotEmpty() && name.isNotBlank(), onClick = { signIn(true) }) { Text("Register at $defaultDomain") }
                TextButton(enabled = !busy, onClick = { advanced = !advanced }) { Text(if (advanced) "Hide invitation setup" else "Use an invitation or another store") }
                if (advanced) {
                Text("For an independent store, enter its HTTPS address and a device invitation from its owner.")
                OutlinedTextField(server, { server = it }, label = { Text("Store address") }, singleLine = true, modifier = Modifier.fillMaxWidth())
                OutlinedTextField(invite, { invite = it }, label = { Text("Invitation") }, visualTransformation = PasswordVisualTransformation(), singleLine = true, modifier = Modifier.fillMaxWidth())
                Button(enabled = !busy && invite.isNotBlank() && name.isNotBlank(), onClick = {
                    busy = true; scope.launch { try {
                        val result = withContext(Dispatchers.IO) { StoreApi(Session(Versions.baseUrl(server), "")).enroll(invite, name) }
                        vault.save(result); session = result; invite = ""; message = "Device enrolled"
                        if (Build.VERSION.SDK_INT >= 33) notifications.launch(Manifest.permission.POST_NOTIFICATIONS)
                    } catch (e: Exception) { message = e.message ?: "Enrollment failed" } finally { busy = false } }
                }) { Text("Enroll this device") }
                }
            } else {
                Text(session!!.baseUrl, style = MaterialTheme.typography.bodySmall)
                if (session!!.account.isNotBlank()) Text("Signed in as ${session!!.account}", style = MaterialTheme.typography.bodyMedium)
                if (session!!.account.isBlank()) {
                    Row(horizontalArrangement = Arrangement.spacedBy(8.dp), verticalAlignment = Alignment.CenterVertically) {
                        OutlinedTextField(username, { username = it }, label = { Text("Username") }, placeholder = { Text("username@$defaultDomain") }, singleLine = true, modifier = Modifier.weight(1f), keyboardOptions = androidx.compose.foundation.text.KeyboardOptions(capitalization = androidx.compose.ui.text.input.KeyboardCapitalization.None, autoCorrectEnabled = false))
                        Button(enabled = !busy && defaultDomain.isNotEmpty() && username.isNotBlank(), onClick = { signIn() }, modifier = Modifier.semantics { contentDescription = "Sign in" }) { Text("🔥") }
                    }
                } else if (defaultIssuer == "https://gravitas.cool" && session!!.account.endsWith(":$defaultDomain") && runCatching { BrokerVault(this@MainActivity).load() }.getOrNull()==null) {
                    TextButton(enabled = !busy, onClick = { signIn() }) { Text("Enable Gravitas sign-in for your apps") }
                }
                FlowRow(horizontalArrangement = Arrangement.spacedBy(12.dp), verticalArrangement = Arrangement.spacedBy(8.dp)) {
                    Button(enabled = !busy, onClick = { refresh() }) { Text("Check for updates") }
                    TextButton(enabled = !busy, onClick = { if (Build.VERSION.SDK_INT >= 33 && !Notices.enabled(this@MainActivity)) notifications.launch(Manifest.permission.POST_NOTIFICATIONS) else startActivity(Intent(Settings.ACTION_APP_NOTIFICATION_SETTINGS).putExtra(Settings.EXTRA_APP_PACKAGE, packageName)) }) { Text("Notifications") }
                }
                if (!installAllowed) OutlinedButton(onClick = { permission.launch(Intent(Settings.ACTION_MANAGE_UNKNOWN_APP_SOURCES, Uri.parse("package:$packageName"))) }) { Text("Allow app installation") }
                Row {
                    Switch(checked = auto, onCheckedChange = { auto = it; prefs.edit().putBoolean("auto", it).apply() })
                    Text("Automatic updates", modifier = Modifier.padding(start = 12.dp, top = 12.dp))
                }
                Row {
                    Switch(checked = appNotifications, onCheckedChange = { appNotifications = it; prefs.edit().putBoolean("appNotifications", it).apply(); NotificationRelay.configure(this@MainActivity, session!!, apps, it) })
                    Text("App notifications", modifier = Modifier.padding(start = 12.dp, top = 12.dp))
                }
                Text("One shared connection receives notifications for installed apps. Each app controls its own notification permission.", style = MaterialTheme.typography.bodySmall)
                if (appNotifications && session!!.account.isNotBlank()) {
                    Text(if (batteryExempt) "Screen-off delivery is allowed. Your phone still needs an internet connection." else "Android can delay notifications while your phone is idle. Allow Private Store to run without battery optimization for screen-off delivery. This may use more battery.", style = MaterialTheme.typography.bodySmall)
                    TextButton(enabled = !busy, onClick = {
                        val settings = if (batteryExempt) Intent(Settings.ACTION_IGNORE_BATTERY_OPTIMIZATION_SETTINGS)
                            else Intent(Settings.ACTION_REQUEST_IGNORE_BATTERY_OPTIMIZATIONS, Uri.parse("package:$packageName"))
                        try { startActivity(settings) }
                        catch (_: ActivityNotFoundException) {
                            try { startActivity(Intent(Settings.ACTION_APPLICATION_DETAILS_SETTINGS, Uri.parse("package:$packageName"))) }
                            catch (_: ActivityNotFoundException) { message = "Open Android settings and allow unrestricted battery use for Private Store." }
                        }
                    }) { Text(if (batteryExempt) "Battery settings" else "Allow screen-off notifications") }
                }
                Text("Checks include Private Store itself and run about every 6 hours when connected and the battery is not low. Android may ask you to approve an update. Notifications must be enabled for background installation.", style = MaterialTheme.typography.bodySmall)
                val checked = prefs.getLong("lastCheck", 0)
                if (checked > 0) Text("Last background check: ${DateFormat.getDateTimeInstance().format(Date(checked))}", style = MaterialTheme.typography.bodySmall)
                apps.forEach { app ->
                    val r = app.release; val installed = remember(app.packageName, revision) { Installer.installed(this@MainActivity, app.packageName) }
                    val update = r != null && Versions.newer(installed, r.versionCode)
                    Card(Modifier.fillMaxWidth()) { Column(Modifier.padding(20.dp), verticalArrangement = Arrangement.spacedBy(10.dp)) {
                        Text(app.name, style = MaterialTheme.typography.titleLarge)
                        Text(app.description)
                        Text(when { r == null -> "No verified release available"; installed == null -> "Version ${r.versionName} · ${DownloadSizes.format(r.size)}"; update -> "Update to ${r.versionName}"; installed > r.versionCode -> "Installed a newer version · code $installed"; else -> "Up to date · ${r.versionName}" }, style = MaterialTheme.typography.labelLarge)
                        if (r != null && r.notes.isNotBlank()) TextButton(onClick = { selected = app }) { Text("Release notes") }
                        Button(enabled = !busy && update && r!!.minSdk <= Build.VERSION.SDK_INT, onClick = {
                            if (!packageManager.canRequestPackageInstalls()) { permission.launch(Intent(Settings.ACTION_MANAGE_UNKNOWN_APP_SOURCES, Uri.parse("package:$packageName"))) }
                            else { val current = session!!; busy = true; downloadFraction = 0f; message = "Downloading ${app.name}…"; scope.launch { try {
                                withContext(Dispatchers.IO) { Installer.install(this@MainActivity, StoreApi(current).download(this@MainActivity, app) { done,total -> scope.launch { downloadFraction = done.toFloat()/total.toFloat() } }, app, background = false) }
                                message = "Follow Android's installation prompt, then check for updates."
                            } catch (e: Exception) { message = e.message ?: "Installation failed" } finally { busy = false } } }
                        }) { Text(if (installed == null) "Install" else if (update && app.packageName == packageName) "Update Private Store" else if (update) "Update" else "Installed") }
                    } }
                }
                TextButton(enabled = !busy, onClick = { busy = true; scope.launch { try {
                    withContext(Dispatchers.IO) { try { StoreApi(session!!).revoke() } catch(e:ApiException) { if(e.code!=401) throw e } }
                    BrokerManager.clear(this@MainActivity,true); UpdateWorker.cancel(this@MainActivity); session = null; apps = emptyList(); message = "Device access revoked"
                } catch (e: Exception) { message = "Could not revoke access: ${e.message}" } finally { busy = false } } }) { Text("Sign out and revoke this device") }
                TextButton(enabled = !busy, onClick = { BrokerManager.clear(this@MainActivity,true); UpdateWorker.cancel(this@MainActivity); session = null; apps = emptyList(); message = "Signed out locally. Ask the administrator to revoke old device access." }) { Text("Forget local enrollment") }
            }
            if (pending != null) Card(Modifier.fillMaxWidth()) { Column(Modifier.padding(20.dp), verticalArrangement = Arrangement.spacedBy(10.dp)) {
                Text("Sign-in is open in your browser", style = MaterialTheme.typography.titleMedium)
                Text("Complete the Gravitas page, then choose Open Private Store if Android does not return here automatically.")
                TextButton(enabled = !busy, onClick = { runCatching { openLogin(pending!!.authorizationUrl) }.onFailure { message = "No browser is available. Install or enable a browser, then try again." } }) { Text("Open sign-in again") }
                TextButton(enabled = !busy, onClick = { vault.clearPending(); pending = null; message = "Sign-in cancelled" }) { Text("Cancel sign-in") }
            } }
            if (busy) { if (downloadFraction > 0f) LinearProgressIndicator(progress = { downloadFraction }, modifier = Modifier.fillMaxWidth()) else LinearProgressIndicator(Modifier.fillMaxWidth()) }
            if (message.isNotBlank()) Text(message, color = MaterialTheme.colorScheme.primary)
        }
        selected?.let { app -> AlertDialog(onDismissRequest = { selected = null }, title = { Text("${app.name} · ${app.release?.versionName}") }, text = { Text(app.release?.notes ?: "", modifier = Modifier.verticalScroll(rememberScrollState())) }, confirmButton = { TextButton(onClick = { selected = null }) { Text("Done") } }) }
    }
}
