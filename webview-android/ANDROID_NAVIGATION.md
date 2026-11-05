# Work and News Android navigation

Follow the [Gravitas navigation contract](https://github.com/jeethsuresh/appstore/tree/main/integrations/navigation).
`app/src/main/java/cool/gravitas/navigation/GravitasEdgeNavigation.java` is an
unchanged copy of that shared component. Keep it synchronized with the canonical
source. Android 14+ left-edge Back and right-edge Forward navigate WebView
destinations; hardware/three-button Back remains Back. Android 8–13 keep platform
Back and expose Forward through the toolbar. No gesture exclusions are used.

History uses the WebView's real page and History API stack. A new destination
discards the forward branch. At 65 entries, prune to the current destination to
bound private history at 64. Forward at the end is a no-op. Cancelling a gesture
does nothing. The shared handler dismisses the keyboard first; Android dialogs
keep their own window's Back handling. HTML dialogs and the hosted apps' modal
backdrops consume Back as cancellation and block Forward. Their existing cancel
handlers retain control over unsaved editor confirmation.

Authentication routes and `/login` clear browsing history. Completing a sign-in
clears authentication history again. While authenticating, Forward does nothing
and Back cancels to `/login?auto=off` rather than replaying a login. Restoring an
activity during sign-in also cancels to that page. Same-origin 401/403 responses
clear history on lost access. The two packages have independent storage and fixed
app origins. The shell never automatically resubmits POST forms, and launches no
install/send/share/call action from a destination history entry.

Run both flavor unit tests, lint, and device tests listed in [WEBVIEW_ANDROID.md](../WEBVIEW_ANDROID.md).
Device fixtures cover branching, multiple Back/Forward steps, empty Forward,
real OS edge gestures, cancellation, hardware Back, restored history, and logout.
Future hosted screens must expose browsing destinations through the History API
or URL hash and validate access when restored. They may cancel the same-origin
`gravitas:navigate` event (detail.direction is `back` or `forward`) to handle their
own transient UI before destination history changes. Navigation never submits
forms or activates action buttons.

News implements these events with its bounded in-memory destination history in
`jeethsuresh/rss`, covering state-based panes and selections as well as page
history. `gravitas:navigation-state` supplies a mutable detail object with `back`
and `forward` booleans. The shell queries it after touches and navigation so the
toolbar also works for hosted state destinations. Dialogs are handled before
state events. This remains a DOM event protocol without a privileged native bridge.

CI runs `tools/e2e/configure-touch-avd.py` before booting to set `hw.mainKeys=no`.
An emulator with hardware navigation keys suppresses Android's software navigation
bar and cannot exercise OS edge gestures even if its navigation-mode setting is 2.
Keep the actual left/right OS gesture assertions and the software-navigation
device configuration when updating CI.
