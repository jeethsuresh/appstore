package store

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"private-store/internal/gravitas"
	"slices"
	"strings"
)

type appFeature struct{ Title, Description string }
type appStory struct {
	Category, Tagline, Architecture, MinimumAndroid string
	Features                                        []appFeature
	LocalData, Connection                           string
}
type webApp struct {
	App
	appStory
	Size, Platforms, DisplayURL string
}
type webView struct {
	SignedIn, Enabled, Error, CatalogAdmin, SelectedGlobal              bool
	Username, Digest, Title, Description, Next, StoreURL, DefaultDomain string
	ClientVersion, ClientSize                                           string
	Apps                                                                []webApp
	Selected                                                            *webApp
}

var appStories = map[string]appStory{
	"rss": {
		Category: "News & feeds", Tagline: "Keep up with the sites you follow.",
		Features: []appFeature{
			{"Build your reading list", "Subscribe to RSS feeds, group them and catch up on new articles from one reader."},
			{"Keep the articles you want", "Save items to revisit and use your local desktop library when your connection drops."},
		},
		LocalData:  "The desktop app keeps subscriptions and reading state in a local database.",
		Connection: "Feed refreshes need a connection. A hosted RSS server is optional on desktop.",
	},
	"email": {
		Category: "Email", Tagline: "An inbox you can take offline.",
		Features: []appFeature{
			{"Catch up away from Wi-Fi", "Read previously downloaded mail without connecting to your mail provider."},
			{"Find a conversation", "Search your local mailbox and sync new messages over IMAP."},
		},
		LocalData:  "Downloaded messages are stored locally on your computer.",
		Connection: "Connect to your mail provider to sync new mail.",
	},
	"office": {
		Category: "Documents & collaboration", Tagline: "Keep related work together.",
		Features: []appFeature{
			{"Work in one place", "Keep notes, canvases, slides and spreadsheets in the same workspace."},
			{"Open a local workspace", "Use the desktop app with its local backend and device storage."},
		},
		LocalData:  "The desktop app stores its local workspace on your computer.",
		Connection: "The hosted Office workspace is available on the web with Gravitas sign-in.",
	},
	"work": {
		Category: "Documents & collaboration", Tagline: "Your workspace, on your phone.",
		Architecture: "All Android architectures", MinimumAndroid: "Android 8.0+",
		Features: []appFeature{
			{"Keep working", "Open the same hosted documents you use on the web. Upload files with Android's file picker."},
			{"Go back and forward", "On Android 14 and later, swipe from the left to go back and from the right to go forward. Navigation buttons are also available."},
		},
		LocalData:  "Sign-in cookies, website storage and cache stay in Work's private Android storage. Android backup is disabled. Your documents remain in the hosted Office service.",
		Connection: "Requires an internet connection and Android System WebView. Sign in inside the app. External links and HTTPS downloads open in your browser; browser sign-in may be needed there. On older Android versions, use the Forward button.",
	},
	"news": {
		Category: "News & feeds", Tagline: "Your feeds, close at hand.",
		Architecture: "All Android architectures", MinimumAndroid: "Android 8.0+",
		Features: []appFeature{
			{"Read your feeds", "Use the hosted reader's subscriptions, articles and saved items. Open external articles in your browser."},
			{"Retrace your steps", "On Android 14 and later, swipe from the left to go back and from the right to go forward. An empty forward history leaves you on the current screen."},
		},
		LocalData:  "Sign-in cookies, website storage and cache stay in News's private Android storage, separate from Work and your browser. Android backup is disabled. Your feeds remain in the hosted RSS service.",
		Connection: "Requires an internet connection and Android System WebView. Sign in inside the app. This wrapper uses the hosted reader rather than the desktop app's local database. On older Android versions, use the Forward button.",
	},
	"morsel": {
		Category: "Food & reflection", Tagline: "Your daily rhythm.",
		Architecture: "ARM64 Android phones", MinimumAndroid: "Android 8.0+",
		Features: []appFeature{
			{"Remember the moments", "Log meals and reflections from reusable templates, browse continuous months, and edit any day’s entries."},
			{"Ask your own questions", "Combine text, number, slider, checkbox and dropdown fields into independent attributes and templates. Attach them to any timeslot or use them on demand."},
			{"A gentle nudge", "Default reminders at noon and midnight have conditional 18:00 and 09:00 catch-ups. Midnight and its catch-up log the previous day. Times follow the phone’s timezone."},
			{"Keep a copy", "Save or share lossless JSON backups and long-form CSV exports including entries, questions, templates, timeslots and reminder logs. Restore personal JSON backups on-device."},
			{"Show the app, keep your privacy", "Demo View replaces the journal with Alex Morgan’s fictional data. Personal notifications pause and demo edits never touch personal storage."},
		},
		LocalData:  "Every journal record stays in an app-private SQLite database. Morsel has no sign-in, backend, analytics or network permission. Android cloud backup and device transfer are disabled; export a JSON backup to keep a copy.",
		Connection: "Morsel works entirely offline. Only Private Store needs a connection to install signed updates. Android notification permission is required for alerts; optional precise alarm access improves timing. Android force-stop pauses delivery until the app is reopened.",
	},
	"haven": {
		Category: "Media & backup", Tagline: "A home for every memory.",
		Architecture: "ARM, ARM64 and x86-64", MinimumAndroid: "Android 8.0+",
		Features: []appFeature{
			{"Choose your source", "Enable your Android gallery as a source and grant access to the media you want to back up. Your phone keeps its originals."},
			{"Keep copies at home", "Enable a desktop as a sink and choose its repository folder. Haven transfers directly between devices and verifies each completed copy."},
			{"See your library", "Browse photo, video and audio thumbnails, search filenames, inspect stored-copy counts and see connected devices."},
		},
		LocalData:  "Original media stays on your source and sink devices. Web viewers cache thumbnails in browser localStorage and fetch missing thumbnails directly from a source or sink over WebRTC. The Haven service stores account-scoped filenames, revision descriptors, transfer status and device reports about pending items and errors. Haven retains previous sink versions when replacing files.",
		Connection: "The initial release scans and transfers while Haven is open. Sign-in and coordination need a connection; restrictive networks may prevent a direct route. Background backup and native contacts/notes adapters are not included yet.",
	},
	"atlas": {
		Category: "Maps & places", Tagline: "Your world, in layers.",
		Architecture: "ARM, ARM64 and x86-64", MinimumAndroid: "Android 8.0+",
		Features: []appFeature{
			{"Make the map yours", "Switch nearby businesses, saved lists, routes and friends on or off independently over an OpenStreetMap base layer."},
			{"Keep your good finds", "Search places and addresses, save businesses into multiple lists, rename collections and remove pins. Sign in with Gravitas to sync lists and highlights across web and Android."},
			{"Remember the way there", "Plan driving directions, save a route, highlight a named street near the map or draw a personal path. Public transit schedules and live traffic are not included."},
			{"Find your people, on your terms", "Connect with single-use invitations. Each friend chooses when to share their foreground location. Sharing pauses when Atlas is hidden; the latest position expires within two minutes. No location history is kept."},
		},
		LocalData:  "Guest lists and highlights stay on the device. Signed-in lists, highlights, accepted friendships and the latest voluntarily shared location are stored by Atlas with account access controls. Removing a friend ends location access in both directions.",
		Connection: "Maps, search, directions, sync and friends need a connection. OpenStreetMap, Nominatim, Photon and OSRM receive the map area or query used for their services. Background location sharing and offline map packs are not included.",
	},
	"river": {
		Category: "Chat & communities", Tagline: "A little closer, wherever you are.",
		Architecture: "ARM, ARM64 and x86-64", MinimumAndroid: "Android 6.0+",
		Features: []appFeature{
			{"Give each topic a home", "Create spaces and separate text channels. Invitations and membership are managed by the space, with saved unread positions and earlier-message history."},
			{"Keep your people close", "Send and accept friend requests, start private conversations, and remove or block people. Blocking stops new direct messages and friend requests."},
			{"Make room for ideas", "Write posts and discussions with Markdown preview, edit your own entries, follow reply threads, and add or remove emoji reactions."},
			{"Use the screen you have", "The shared Flutter client adapts to phones, tablets and desktops. Continue on the web at chat.gravitas.cool."},
		},
		LocalData:  "River keeps its Android sign-in in secure device storage. Gravitas stores access-controlled group membership, message history and social events; central sign-out revokes connected sessions.",
		Connection: "Sign-in, history and new messages require a connection to Gravitas. Voice, video, screen sharing and file attachments are not enabled in this release.",
	},
	"launcher": {
		Category: "Home screen", Tagline: "A home that feels like yours.",
		Architecture: "ARM, ARM64 and x86-64", MinimumAndroid: "Android 8.0+",
		Features: []appFeature{
			{"Keep apps together", "Drag an app onto another to create a folder, on a home screen or in the dock. Drop more apps onto it, rename it, or drag apps back out."},
			{"Make it your own", "Choose an image wallpaper with a preview before applying. Set separate colours and opacity for the app drawer and dock."},
			{"Reach your apps", "Search at the bottom of the drawer. Hold an app and drag it below the dock to hide it or ask Android to uninstall it."},
			{"Build your home", "Add resizable widgets, change the grid and icon size, and drag to either edge to add home pages. Still Keyboard is included."},
		},
		LocalData:  "Home layout, folders, hidden apps and appearance settings stay on your device. The launcher needs no account and has no network permission.",
		Connection: "The launcher works offline. Private Store needs a connection to download signed updates; Android controls app removal and widget permissions.",
	},
	"appstore": {
		Category: "Apps & updates", Tagline: "A small home for your apps.",
		Architecture: "ARM, ARM64 and x86-64", MinimumAndroid: "Android 8.0+",
		Features: []appFeature{
			{"Keep the store current", "New signed versions of Private Store appear alongside your other app updates. Check now or let periodic checks find them; Android may ask you to approve installation."},
			{"Keep your settings", "Updating the store preserves its sign-in and update preferences. Every APK must match the configured package, version, checksum and permanent release certificate."},
			{"Sign in with Gravitas", "Use your Gravitas account to enroll your phone and connect supported apps. Central sign-out revokes connected sessions."},
		},
		LocalData:  "Your enrollment and Gravitas credentials are encrypted with keys held by Android. Store updates retain those keys, your account and your update settings.",
		Connection: "Browsing, downloads and sign-in need a connection. Update checks run about every six hours when Android's network and battery conditions allow. You control app installation and notification permissions.",
	},
	"daybreak": {
		Category: "Weather", Tagline: "A clearer view of your day.",
		Architecture: "ARM64 Android phones", MinimumAndroid: "Android 7.0+",
		Features: []appFeature{
			{"See the hours ahead", "Explore hourly temperatures and rain chances, a fixed 24-hour forecast graph, and a compact daily outlook with highs and lows."},
			{"Choose your weather source", "Use Environment Canada in Canada, the US National Weather Service in the United States, or Open-Meteo worldwide. Automatic mode can fall back when a source is unavailable."},
			{"Keep weather on your home screen", "Choose a 1×1, 2×1 or 2×2 widget. See the temperature, conditions and, on the larger widget, a small forecast graph without opening the app."},
			{"Find your place", "Search for a city or choose your current location. Manual city selection is always available, including when you prefer not to share location."},
			{"Make room for a smaller screen", "A compact layout adapts to short windows and foldable cover screens, with a connected hourly strip and a readable daily list."},
			{"Know when to look closer", "Read official weather alerts when supplied by supported Canadian and US feeds. Open-Meteo does not provide those alerts; Daybreak does not send alert notifications."},
		},
		LocalData:  "Daybreak needs no account. Your settings and saved forecasts stay in the app’s storage on your phone. The last successful forecast remains readable when a refresh fails.",
		Connection: "Fresh forecasts need an internet connection. Weather providers receive your IP address and the requested coordinates; city search sends your search text. Optional hourly refresh can be delayed by Android’s battery and network rules.",
	},
	"spacetime": {
		Category: "Calendar & tasks", Tagline: "Make room for what matters.",
		Architecture: "ARM, ARM64 and x86-64", MinimumAndroid: "Android 6.0+",
		Features: []appFeature{
			{"See your time, your way", "Move between daily and weekly time grids, a month view and a full year. Overlapping appointments sit side by side, with all-day entries above the grid."},
			{"Keep more than appointments", "Create events, todos with due dates, birthdays and other occasions. Search your entries and choose which types appear on the calendar."},
			{"Let plans repeat", "Set up recurring entries, skip individual occurrences and track repeating todos. Calendar entries keep their time zones and exceptions."},
			{"Remember the small things", "Choose local reminders for appointments and due dates. Android handles notification and alarm permissions; reminders do not require a push service."},
			{"Bring your day to your home screen", "Add Next appointment, Today or Week ahead widgets. They read your saved device calendar and open the relevant view when tapped."},
			{"Connect when you choose", "Use an optional Spacetime server for sync, invitations and CalDAV access. Your calendar remains usable when you are offline or disconnect the server."},
		},
		LocalData:  "The calendar opens without an account. Events, todos, occasions, search and display settings work locally, with entries saved in app-private storage on your device.",
		Connection: "Server sync and invitations need a connection and an account on that server. Local reminders and widgets work from saved device data. CalDAV is provided by the optional server and requires a compatible account.",
	},
}

func downloadSize(bytes int64) string {
	if bytes < 1000 {
		return fmt.Sprintf("%d B", bytes)
	}
	if bytes < 1e6 {
		return fmt.Sprintf("%.1f kB", float64(bytes)/1e3)
	}
	return fmt.Sprintf("%.1f MB", float64(bytes)/1e6)
}

func (s *Server) webApps(ctx context.Context, storeURL string) ([]webApp, error) {
	catalog := s.catalogApps()
	apps := make([]webApp, 0, len(catalog))
	for _, configured := range catalog {
		app := configured
		release, err := s.latest(ctx, app.ID)
		if err != nil {
			return nil, err
		}
		if release != nil && release.ConfigHash != appTrust(app) {
			release = nil
		}
		app.Release = release
		story, ok := appStories[app.ID]
		if !ok {
			story = appStory{Architecture: "See available builds", MinimumAndroid: "See the Android store"}
		}
		desktop, err := s.latestDesktop(ctx, app)
		if err != nil {
			return nil, err
		}
		if desktop != nil {
			desktop.Artifacts = slices.DeleteFunc(desktop.Artifacts, func(artifact DesktopArtifact) bool {
				return !app.allowsDesktopArtifact(artifact.Platform, desktop.VersionCode)
			})
			if len(desktop.Artifacts) == 0 {
				desktop = nil
			}
		}
		if app.Desktop {
			app.DesktopRelease = desktop
		}
		item := webApp{App: app, appStory: story, DisplayURL: app.Website}
		if item.DisplayURL == "" {
			item.DisplayURL = storeURL + "/apps/" + app.ID
		}
		item.DisplayURL = strings.TrimPrefix(item.DisplayURL, "https://")
		platforms := []string{}
		if app.Package != "" {
			platforms = append(platforms, "Android")
		}
		if app.Desktop {
			found := map[string]bool{}
			if desktop != nil {
				for _, artifact := range desktop.Artifacts {
					found[artifact.Platform] = true
				}
			}
			for _, pair := range [][2]string{{"macos", "macOS"}, {"windows", "Windows"}, {"linux", "Linux"}} {
				if found[pair[0]] {
					platforms = append(platforms, pair[1])
				}
			}
			if desktop == nil {
				platforms = append(platforms, "Desktop (release pending)")
			}
		}
		switch app.IOS {
		case "testflight":
			platforms = append(platforms, "iOS (TestFlight)")
		case "app-store":
			platforms = append(platforms, "iOS")
		}
		item.Platforms = strings.Join(platforms, " · ")
		if app.Package == "" && app.Desktop {
			item.Architecture = "See available desktop builds"
			item.MinimumAndroid = "Desktop"
		}
		if release != nil {
			item.Size = downloadSize(release.Size)
			versions := map[int]string{21: "5.0", 22: "5.1", 23: "6.0", 24: "7.0", 25: "7.1", 26: "8.0", 27: "8.1", 28: "9", 29: "10", 30: "11", 31: "12", 32: "12L", 33: "13", 34: "14", 35: "15", 36: "16"}
			if version, found := versions[release.MinSDK]; found {
				item.MinimumAndroid = "Android " + version + "+"
			} else {
				item.MinimumAndroid = fmt.Sprintf("Android API %d+", release.MinSDK)
			}
		}
		apps = append(apps, item)
	}
	return apps, nil
}
func (s *Server) webData(w http.ResponseWriter, r *http.Request) (webView, error) {
	data := webView{Enabled: s.cfg.Federation != nil, Error: r.URL.Query().Get("error") != "", Digest: clientSHA256, ClientVersion: clientVersion, ClientSize: downloadSize(clientSize), Title: "Private Store · Gravitas", Description: "A small home for independent apps on Android, macOS, Windows and Linux. Get Private Store with your Gravitas account.", Next: "/", StoreURL: "https://apps.gravitas.cool"}
	if s.cfg.Federation != nil {
		data.DefaultDomain = s.cfg.Federation.DefaultDomain()
	}
	if s.cfg.Federation != nil {
		var identity gravitas.Identity
		var err error
		identity, err = s.cfg.Federation.Session(r)
		if errors.Is(err, gravitas.ErrUnavailable) {
			return data, err
		}
		if err != nil {
			if _, cookieErr := r.Cookie(gravitas.SessionCookie); cookieErr == nil {
				s.cfg.Federation.ClearSession(w)
			}
		}
		data.SignedIn, data.Username, data.StoreURL = identity.Subject != "", identity.Name, s.cfg.Federation.AppURL
		if data.SignedIn && strings.TrimSpace(data.Username) == "" {
			data.Username = "Gravitas account"
		}
	}
	var err error
	data.Apps, err = s.webApps(r.Context(), data.StoreURL)
	return data, err
}
func (s *Server) webAppPage(w http.ResponseWriter, r *http.Request) {
	data, err := s.webData(w, r)
	if err != nil {
		s.webError(w, err)
		return
	}
	for i := range data.Apps {
		if data.Apps[i].ID != r.PathValue("app") {
			continue
		}
		data.Selected = &data.Apps[i]
		if data.SignedIn && s.cfg.PersonalCatalog {
			data.CatalogAdmin, err = s.catalogAdmin(r)
			if err != nil {
				s.webError(w, err)
				return
			}
			if data.CatalogAdmin {
				err = s.db.QueryRowContext(r.Context(), "SELECT EXISTS (SELECT 1 FROM catalog_global_apps WHERE app=?)", data.Selected.ID).Scan(&data.SelectedGlobal)
				if err != nil {
					s.webError(w, err)
					return
				}
			}
		}
		data.Title = data.Selected.Name + " · Private Store"
		data.Description = data.Selected.Description
		data.Next = "/apps/" + data.Selected.ID
		s.renderWeb(w, "app.html", data)
		return
	}
	http.NotFound(w, r)
}
func (s *Server) renderWeb(w http.ResponseWriter, name string, data webView) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Content-Security-Policy", "default-src 'none'; style-src 'self'; script-src 'self'; img-src 'self'; connect-src 'self'; base-uri 'none'; frame-ancestors 'none'; form-action 'self'")
	w.Header().Set("Referrer-Policy", "no-referrer")
	_ = webPage.ExecuteTemplate(w, name, data)
}
