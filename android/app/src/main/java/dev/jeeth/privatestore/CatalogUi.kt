package dev.jeeth.privatestore

import android.os.Build
import androidx.activity.compose.BackHandler
import androidx.compose.foundation.Image
import androidx.compose.foundation.selection.toggleable
import androidx.compose.ui.semantics.Role
import androidx.compose.ui.platform.LocalContext
import androidx.compose.foundation.layout.*
import androidx.compose.foundation.lazy.grid.*
import androidx.compose.foundation.rememberScrollState
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.foundation.verticalScroll
import androidx.compose.material3.*
import androidx.compose.runtime.*
import androidx.compose.runtime.saveable.rememberSaveable
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.res.painterResource
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.text.style.TextOverflow
import androidx.compose.ui.unit.dp

@Composable
fun AppIcon(app: StoreApp, modifier: Modifier = Modifier) {
    val icon = when (app.id) {
        "atlas" -> R.drawable.app_atlas
        "daybreak" -> R.drawable.app_daybreak
        "spacetime" -> R.drawable.app_spacetime
        "river" -> R.drawable.app_river
        "launcher" -> R.drawable.app_launcher
        "haven" -> R.drawable.app_haven
        else -> R.drawable.app_icon
    }
    Image(painterResource(icon), contentDescription = null, modifier = modifier.size(56.dp))
}

private fun availability(app: StoreApp, installed: Long?): String {
    val release = app.release ?: return "No verified release available"
    return when {
        release.minSdk > Build.VERSION.SDK_INT -> "Requires Android API ${release.minSdk}+"
        installed == null -> "Version ${release.versionName} · ${DownloadSizes.format(release.size)}"
        installed > release.versionCode -> "Installed a newer version · code $installed"
        installed == release.versionCode -> "Up to date · ${release.versionName}"
        else -> "Update to ${release.versionName}"
    }
}

@Composable
fun AppAction(app: StoreApp, installed: Long?, transfer: Downloads.Transfer?, pending: Boolean, onInstall: () -> Unit) {
    val release = app.release
    val active = transfer?.active == true
    val update = release != null && Versions.newer(installed, release.versionCode)
    Button(onClick = onInstall, enabled = !active && update && release!!.minSdk <= Build.VERSION.SDK_INT, contentPadding = PaddingValues(horizontal = 18.dp, vertical = 8.dp)) {
        Text(when { active -> transfer!!.label; pending -> "Retry install"; installed == null -> "Install"; update -> "Update"; else -> "Installed" })
    }
    if (active) {
        transfer?.fraction?.let { LinearProgressIndicator(progress = { it }, modifier = Modifier.fillMaxWidth()) }
            ?: LinearProgressIndicator(Modifier.fillMaxWidth())
    }
    if (transfer != null && !active && update) Text(if (pending) "Finish installing in Android, or retry using the saved download." else transfer.label, style = MaterialTheme.typography.bodySmall)
}

@Composable
fun StoreCatalog(
    apps: List<StoreApp>, installed: Map<String, Long?>, transfers: Map<String, Downloads.Transfer>, pending: Set<String>, busy: Boolean,
    message: String, onRefresh: () -> Unit, onInstall: (List<StoreApp>) -> Unit, settings: @Composable ColumnScope.() -> Unit
) {
    var page by rememberSaveable { mutableStateOf("apps") }
    var selectedId by rememberSaveable { mutableStateOf<String?>(null) }
    var query by rememberSaveable { mutableStateOf("") }
    val context = LocalContext.current
    val preferences = remember { context.getSharedPreferences("catalog", android.content.Context.MODE_PRIVATE) }
    var list by rememberSaveable { mutableStateOf(preferences.getBoolean("list", false)) }
    var filter by rememberSaveable { mutableStateOf("All apps") }
    val selected = apps.firstOrNull { it.id == selectedId }
    val updates = apps.filter { installed[it.packageName] != null && it.release?.let { r -> Versions.newer(installed[it.packageName], r.versionCode) && r.minSdk <= Build.VERSION.SDK_INT } == true }
    BackHandler(selectedId != null || page != "apps") { if (selectedId != null) selectedId = null else page = "apps" }
    Scaffold(
        bottomBar = {
            NavigationBar(containerColor = MaterialTheme.colorScheme.surface) {
                NavigationBarItem(selected = page == "apps", onClick = { page = "apps"; selectedId = null }, icon = { UiSymbol("apps") }, label = { Text("Apps") })
                NavigationBarItem(selected = page == "settings", onClick = { page = "settings"; selectedId = null }, icon = { UiSymbol("settings") }, label = { Text("Settings") })
            }
        }
    ) { insets ->
        Column(Modifier.fillMaxSize().padding(insets).consumeWindowInsets(insets)) {
            Row(Modifier.fillMaxWidth().padding(horizontal = 24.dp, vertical = 14.dp), verticalAlignment = Alignment.CenterVertically) {
                if (selected != null) IconButton(onClick = { selectedId = null }) { UiSymbol("back", "Back to apps") }
                Column(Modifier.weight(1f)) {
                    Text("PRIVATE STORE", style = MaterialTheme.typography.labelSmall, color = MaterialTheme.colorScheme.primary, letterSpacing = androidx.compose.ui.unit.TextUnit(2f, androidx.compose.ui.unit.TextUnitType.Sp))
                    Text(if (page == "settings") "Settings" else if (selected != null) "App details" else "Your collection", style = MaterialTheme.typography.headlineMedium, fontWeight = FontWeight.SemiBold)
                }
                if (page == "apps" && selected == null) IconButton(onClick = { list = !list; preferences.edit().putBoolean("list", list).apply() }) { UiSymbol(if (list) "apps" else "list", if (list) "Show two columns" else "Show list") }
            }
            if (busy) LinearProgressIndicator(Modifier.fillMaxWidth())
            if (message.isNotBlank()) Surface(color = MaterialTheme.colorScheme.secondaryContainer, modifier = Modifier.fillMaxWidth().padding(horizontal = 24.dp, vertical = 4.dp), shape = RoundedCornerShape(12.dp)) {
                Text(message, Modifier.padding(12.dp), style = MaterialTheme.typography.bodySmall)
            }
            when {
                page == "settings" -> Column(Modifier.fillMaxSize().verticalScroll(rememberScrollState()).padding(24.dp), verticalArrangement = Arrangement.spacedBy(16.dp), content = settings)
                selected != null -> Column(Modifier.fillMaxSize().verticalScroll(rememberScrollState()).padding(24.dp), verticalArrangement = Arrangement.spacedBy(20.dp)) {
                    AppIcon(selected, Modifier.size(88.dp))
                    Text(selected.name, style = MaterialTheme.typography.headlineLarge, fontWeight = FontWeight.Bold)
                    Text(availability(selected, installed[selected.packageName]), style = MaterialTheme.typography.labelLarge, color = MaterialTheme.colorScheme.primary)
                    AppAction(selected, installed[selected.packageName], transfers[selected.packageName], selected.packageName in pending) { onInstall(listOf(selected)) }
                    HorizontalDivider()
                    Text("About this app", style = MaterialTheme.typography.titleLarge)
                    Text(selected.description, style = MaterialTheme.typography.bodyLarge)
                    Text("Release notes", style = MaterialTheme.typography.titleLarge)
                    Text(selected.release?.notes?.takeIf { it.isNotBlank() } ?: "Release notes are not available for this version.")
                    HorizontalDivider()
                    Text("App information", style = MaterialTheme.typography.titleMedium)
                    selected.release?.let { Text("Version ${it.versionName} · ${DownloadSizes.format(it.size)}\nRequires Android API ${it.minSdk}+") }
                    Text(selected.packageName, style = MaterialTheme.typography.bodySmall)
                    Text("Every installation is checked against its package, version, checksum and release certificate.", style = MaterialTheme.typography.bodySmall)
                }
                else -> {
                    OutlinedTextField(query, { query = it }, modifier = Modifier.fillMaxWidth().padding(horizontal = 24.dp, vertical = 8.dp), placeholder = { Text("Search your apps") }, label = { Text("Search apps") }, singleLine = true, shape = RoundedCornerShape(16.dp))
                    Row(Modifier.fillMaxWidth().padding(horizontal = 24.dp), horizontalArrangement = Arrangement.spacedBy(8.dp), verticalAlignment = Alignment.CenterVertically) {
                        FilterChip(selected = filter == "All apps", onClick = { filter = "All apps" }, label = { Text("All apps") })
                        FilterChip(selected = filter == "Updates", onClick = { filter = "Updates" }, label = { Text("Updates (${updates.size})") })
                        Spacer(Modifier.weight(1f))
                        IconButton(onClick = onRefresh, enabled = !busy) { UiSymbol("refresh", "Check for updates") }
                    }
                    if (updates.isNotEmpty()) Row(Modifier.fillMaxWidth().padding(horizontal = 24.dp), verticalAlignment = Alignment.CenterVertically) {
                        Text("${updates.size} updates available", modifier = Modifier.weight(1f), style = MaterialTheme.typography.bodySmall)
                        TextButton(onClick = { onInstall(updates.filter { it.packageName !in pending }) }, enabled = updates.any { transfers[it.packageName]?.active != true && it.packageName !in pending }) { Text("Update all") }
                    }
                    val downloading = transfers.count { it.value.active }
                    if (downloading > 0) Text("$downloading downloads in progress", Modifier.padding(horizontal = 24.dp, vertical = 4.dp), style = MaterialTheme.typography.labelMedium, color = MaterialTheme.colorScheme.primary)
                    val visible = (if (filter == "Updates") updates else apps).filter { query.isBlank() || it.name.contains(query, true) || it.description.contains(query, true) }
                    LazyVerticalGrid(columns = GridCells.Fixed(if (list) 1 else 2), contentPadding = PaddingValues(24.dp), horizontalArrangement = Arrangement.spacedBy(12.dp), verticalArrangement = Arrangement.spacedBy(12.dp), modifier = Modifier.weight(1f)) {
                        if (visible.isEmpty()) item(span = { GridItemSpan(maxLineSpan) }) {
                            Column(Modifier.fillMaxWidth().padding(vertical = 40.dp), verticalArrangement = Arrangement.spacedBy(8.dp)) {
                                Text(if (apps.isEmpty()) "Your apps will appear here" else if (filter == "Updates" && query.isBlank()) "Everything is up to date" else "No matching apps", style = MaterialTheme.typography.titleLarge)
                                Text(if (apps.isEmpty()) "Check your connection, then check for updates." else "Try another search or browse all apps.")
                            }
                        }
                        items(visible, key = { it.id }) { app ->
                            OutlinedCard(onClick = { selectedId = app.id }, shape = RoundedCornerShape(20.dp), colors = CardDefaults.outlinedCardColors(containerColor = MaterialTheme.colorScheme.surface)) {
                                Column(Modifier.fillMaxWidth().padding(16.dp), verticalArrangement = Arrangement.spacedBy(12.dp)) {
                                    Row(verticalAlignment = Alignment.CenterVertically, horizontalArrangement = Arrangement.spacedBy(16.dp)) {
                                        AppIcon(app)
                                        if (list) Column(Modifier.weight(1f)) { Text(app.name, style = MaterialTheme.typography.titleMedium, fontWeight = FontWeight.SemiBold); Text(availability(app, installed[app.packageName]), style = MaterialTheme.typography.bodySmall) }
                                    }
                                    if (!list) { Text(app.name, style = MaterialTheme.typography.titleMedium, fontWeight = FontWeight.SemiBold, minLines = 2, maxLines = 2, overflow = TextOverflow.Ellipsis); Text(availability(app, installed[app.packageName]), style = MaterialTheme.typography.bodySmall, minLines = 2) }
                                    AppAction(app, installed[app.packageName], transfers[app.packageName], app.packageName in pending) { onInstall(listOf(app)) }
                                }
                            }
                        }
                    }
                }
            }
        }
    }
}

@Composable
private fun UiSymbol(name: String, description: String? = null) {
    val path = when (name) {
        "apps" -> "M3,3h7v7H3zM14,3h7v7h-7zM3,14h7v7H3zM14,14h7v7h-7z"
        "list" -> "M4,5h16M4,12h16M4,19h16"
        "back" -> "M19,12H5M12,5l-7,7 7,7"
        "refresh" -> "M20,7V3m0,4h-4M20,7a9,9 0,1 0,1,8"
        else -> "M9,3h6l1,4 4,1v8l-4,1 -1,4H9l-1,-4 -4,-1V8l4,-1zM15,12a3,3 0,1 1,-6,0a3,3 0,1 1,6,0"
    }
    val vector = remember(name) { androidx.compose.ui.graphics.vector.ImageVector.Builder(defaultWidth = 24.dp, defaultHeight = 24.dp, viewportWidth = 24f, viewportHeight = 24f).apply {
        addPath(androidx.compose.ui.graphics.vector.PathParser().parsePathString(path).toNodes(), fill = if (name == "apps") androidx.compose.ui.graphics.SolidColor(androidx.compose.ui.graphics.Color.Black) else null, stroke = androidx.compose.ui.graphics.SolidColor(androidx.compose.ui.graphics.Color.Black), strokeLineWidth = 1.6f)
    }.build() }
    Icon(vector, contentDescription = description, modifier = Modifier.size(24.dp))
}

@Composable
fun SettingsToggle(title: String, description: String, checked: Boolean, onChecked: (Boolean) -> Unit) {
    Row(Modifier.fillMaxWidth().toggleable(value = checked, role = Role.Switch, onValueChange = onChecked).padding(vertical = 8.dp), verticalAlignment = Alignment.CenterVertically, horizontalArrangement = Arrangement.spacedBy(16.dp)) {
        Column(Modifier.weight(1f)) {
            Text(title, style = MaterialTheme.typography.titleMedium)
            Text(description, style = MaterialTheme.typography.bodySmall, color = MaterialTheme.colorScheme.onSurfaceVariant)
        }
        Switch(checked = checked, onCheckedChange = null)
    }
}
