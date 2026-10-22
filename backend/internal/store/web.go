package store

import (
	"crypto/sha256"
	"embed"
	"encoding/hex"
	"errors"
	"fmt"
	"html/template"
	"io"
	"net/http"
	"os"
	"private-store/internal/gravitas"
)

// The bootstrap client is pinned to the signed, published release. Updating it
// requires updating the image download and this pin together.
const clientVersion = "0.2.0"
const clientSHA256 = "e71139b5d49e6e0eaae996a316b0c9e9bb90583f651b2f6ed39b5bd4d861511e"
const clientSize int64 = 7585764

//go:embed web/*
var webFiles embed.FS
var webPage = template.Must(template.New("web").Funcs(template.FuncMap{"addOne": func(i int) int { return i + 1 }}).ParseFS(webFiles, "web/index.html", "web/app.html"))

func verifyClientAPK(path, digest string, size int64) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	h := sha256.New()
	n, err := io.Copy(h, f)
	if err != nil {
		return err
	}
	if n != size || hex.EncodeToString(h.Sum(nil)) != digest {
		return errors.New("bootstrap APK does not match the published release")
	}
	return nil
}

func (s *Server) registerWeb() error {
	if s.cfg.Federation != nil {
		if err := verifyClientAPK(s.cfg.ClientAPK, clientSHA256, clientSize); err != nil {
			return fmt.Errorf("verify bootstrap client: %w", err)
		}
	}
	s.mux.HandleFunc("GET /{$}", s.webHome)
	s.mux.HandleFunc("GET /login", s.webHome)
	s.mux.HandleFunc("GET /apps/{app}", s.webAppPage)
	for _, name := range []string{"style.css", "app.js", "icon.svg", "daybreak.svg", "spacetime.svg"} {
		data, _ := webFiles.ReadFile("web/" + name)
		mime := map[string]string{"style.css": "text/css; charset=utf-8", "app.js": "text/javascript; charset=utf-8", "icon.svg": "image/svg+xml", "daybreak.svg": "image/svg+xml", "spacetime.svg": "image/svg+xml"}[name]
		s.mux.HandleFunc("GET /assets/"+name, func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", mime)
			_, _ = w.Write(data)
		})
	}
	s.mux.HandleFunc("GET /auth/gravitas/start", func(w http.ResponseWriter, r *http.Request) { s.webStart(w, r, false) })
	s.mux.HandleFunc("GET /auth/gravitas/register", func(w http.ResponseWriter, r *http.Request) { s.webStart(w, r, true) })
	s.mux.HandleFunc("GET /auth/gravitas/callback", s.webCallback)
	s.mux.HandleFunc("POST /auth/gravitas/logout", s.webLogout)
	s.mux.HandleFunc("GET /auth/gravitas/session", s.webSession)
	s.mux.HandleFunc("GET /download/appstore.apk", s.webDownload)
	return nil
}
func (s *Server) webHome(w http.ResponseWriter, r *http.Request) {
	data, err := s.webData(w, r)
	if err != nil {
		s.webError(w, err)
		return
	}
	s.renderWeb(w, "index.html", data)
}
func (s *Server) webStart(w http.ResponseWriter, r *http.Request, register bool) {
	if s.cfg.Federation == nil {
		fail(w, 503, "Gravitas sign-in is not configured")
		return
	}
	if err := s.cfg.Federation.Start(w, r, register); err != nil {
		fail(w, 503, "Gravitas sign-in is temporarily unavailable. Please try again.")
	}
}
func (s *Server) webCallback(w http.ResponseWriter, r *http.Request) {
	if s.cfg.Federation == nil {
		fail(w, 503, "Gravitas sign-in is not configured")
		return
	}
	identity, next, err := s.cfg.Federation.Callback(w, r)
	if err != nil {
		if errors.Is(err, gravitas.ErrUnavailable) {
			fail(w, 503, "Gravitas is temporarily unavailable")
			return
		}
		http.Redirect(w, r, "/login?error=signin", http.StatusSeeOther)
		return
	}
	if err = s.cfg.Federation.SetSession(w, identity); err != nil {
		fail(w, 500, "Unable to create a session")
		return
	}
	http.Redirect(w, r, gravitas.SafeNext(next), http.StatusSeeOther)
}
func (s *Server) webLogout(w http.ResponseWriter, r *http.Request) {
	if s.cfg.Federation == nil {
		fail(w, 503, "Gravitas sign-in is not configured")
		return
	}
	if !s.cfg.Federation.ValidMutation(r) {
		fail(w, 403, "invalid logout request")
		return
	}
	s.cfg.Federation.ClearSession(w)
	w.WriteHeader(http.StatusNoContent)
}
func (s *Server) webDownload(w http.ResponseWriter, r *http.Request) {
	if s.cfg.Federation == nil {
		fail(w, 503, "Gravitas sign-in is not configured")
		return
	}
	if _, err := s.cfg.Federation.Session(r); err != nil {
		if errors.Is(err, gravitas.ErrUnavailable) {
			fail(w, 503, "Gravitas is temporarily unavailable")
			return
		}
		s.cfg.Federation.ClearSession(w)
		fail(w, 401, "Sign in with Gravitas to download Private Store")
		return
	}
	f, err := os.Open(s.cfg.ClientAPK)
	if err != nil {
		fail(w, 503, "Download is temporarily unavailable")
		return
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		fail(w, 503, "Download is temporarily unavailable")
		return
	}
	w.Header().Set("Content-Type", "application/vnd.android.package-archive")
	w.Header().Set("Content-Disposition", `attachment; filename="appstore-android.apk"`)
	w.Header().Set("Cache-Control", "private, no-store")
	http.ServeContent(w, r, "appstore-android.apk", info.ModTime(), f)
}

func (s *Server) webError(w http.ResponseWriter, err error) {
	if errors.Is(err, gravitas.ErrUnavailable) {
		fail(w, 503, "Gravitas is temporarily unavailable")
		return
	}
	s.internal(w, err)
}

func (s *Server) webSession(w http.ResponseWriter, r *http.Request) {
	if s.cfg.Federation == nil {
		fail(w, 503, "Gravitas is temporarily unavailable")
		return
	}
	identity, expiry, err := s.cfg.Federation.SessionInfo(r)
	if err != nil {
		if errors.Is(err, gravitas.ErrUnavailable) {
			fail(w, 503, "Gravitas is temporarily unavailable")
			return
		}
		s.cfg.Federation.ClearSession(w)
		fail(w, 401, "Sign in with Gravitas again")
		return
	}
	reply(w, 200, map[string]any{"active": true, "account": identity.Username, "expiresAt": expiry.Unix()})
}
