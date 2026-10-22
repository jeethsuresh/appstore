package store

import (
	"context"
	"fmt"
	"net/http"
	"private-store/internal/gravitas"
)

type appFeature struct{ Title, Description string }
type appStory struct {
	Category, Tagline, Summary, Icon, Architecture, MinimumAndroid string
	Features                                                       []appFeature
	LocalData, Connection                                          string
}
type webApp struct {
	App
	appStory
	Size string
}
type webView struct {
	SignedIn, Enabled, Error                             bool
	Username, Digest, Title, Description, Next, StoreURL string
	ClientVersion, ClientSize                            string
	Apps                                                 []webApp
	Selected                                             *webApp
}

var appStories = map[string]appStory{
	"daybreak": {
		Category: "Weather", Tagline: "A clearer view of your day.",
		Summary: "Hourly forecasts, daily outlooks and home-screen widgets. The weather you need, in a quiet little app that keeps your last forecast close.",
		Icon:    "daybreak.svg", Architecture: "ARM64 Android phones", MinimumAndroid: "Android 7.0+",
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
		Summary: "A calendar that starts with your device. Keep events, todos and occasions together, work offline, and connect a server only when you want sync and invitations.",
		Icon:    "spacetime.svg", Architecture: "ARM, ARM64 and x86-64", MinimumAndroid: "Android 6.0+",
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

func (s *Server) webApps(ctx context.Context) ([]webApp, error) {
	apps := make([]webApp, 0, len(s.cfg.Apps))
	for _, configured := range s.cfg.Apps {
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
			story = appStory{Category: "Independent app", Tagline: app.Name, Summary: app.Description, Icon: "icon.svg", Architecture: "See the Android store for compatibility", MinimumAndroid: "See the Android store"}
		}
		item := webApp{App: app, appStory: story}
		if release != nil {
			item.Size = fmt.Sprintf("%.1f MB", float64(release.Size)/1e6)
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
func (s *Server) webData(r *http.Request) (webView, error) {
	data := webView{Enabled: s.cfg.Federation != nil, Error: r.URL.Query().Get("error") != "", Digest: clientSHA256, ClientVersion: clientVersion, ClientSize: fmt.Sprintf("%.1f MB", float64(clientSize)/1e6), Title: "Private Store · Gravitas", Description: "A small home for independent Android apps. Get Private Store with your Gravitas account.", Next: "/", StoreURL: "https://apps.gravitas.cool"}
	if s.cfg.Federation != nil {
		var identity gravitas.Identity
		identity, _ = s.cfg.Federation.Session(r)
		data.SignedIn, data.Username, data.StoreURL = identity.Subject != "", identity.Username, s.cfg.Federation.AppURL
	}
	var err error
	data.Apps, err = s.webApps(r.Context())
	return data, err
}
func (s *Server) webAppPage(w http.ResponseWriter, r *http.Request) {
	data, err := s.webData(r)
	if err != nil {
		s.internal(w, err)
		return
	}
	for i := range data.Apps {
		if data.Apps[i].ID != r.PathValue("app") {
			continue
		}
		data.Selected = &data.Apps[i]
		data.Title = data.Selected.Name + " · Private Store"
		data.Description = data.Selected.Summary
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
