package main

import (
	"context"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"private-store/internal/gravitas"
	"private-store/internal/store"
	"syscall"
	"time"
)

func env(key, def string) string {
	if s := os.Getenv(key); s != "" {
		return s
	}
	return def
}
func main() {
	if len(os.Args) > 1 && os.Args[1] == "healthcheck" {
		client := http.Client{Timeout: 3 * time.Second}
		response, err := client.Get(env("HEALTHCHECK_URL", "http://127.0.0.1:8080/healthz"))
		if err != nil {
			os.Exit(1)
		}
		response.Body.Close()
		if response.StatusCode != 200 {
			os.Exit(1)
		}
		return
	}

	apps, err := store.LoadApps(env("APPS_CONFIG", "config/apps.json"))
	if err != nil {
		slog.Error("load apps", "error", err)
		os.Exit(1)
	}
	var federation *gravitas.Client
	if issuer := os.Getenv("GRAVITAS_ISSUER"); issuer != "" {
		federation, err = gravitas.New(issuer, os.Getenv("GRAVITAS_APP_URL"), os.Getenv("GRAVITAS_SESSION_KEY"))
		if err != nil {
			slog.Error("configure Gravitas login", "error", err)
			os.Exit(1)
		}
	} else if os.Getenv("GRAVITAS_REQUIRED") == "true" {
		slog.Error("GRAVITAS_ISSUER is required")
		os.Exit(1)
	}
	notificationKeys, err := store.NotificationKeys(os.Getenv("NOTIFICATION_APP_KEYS"))
	if err != nil {
		slog.Error("notification configuration", "error", err)
		os.Exit(1)
	}
	s, err := store.New(store.Config{NotificationURL: os.Getenv("NOTIFICATION_PUBLIC_URL"), NotificationKeys: notificationKeys, DataDir: env("DATA_DIR", "data"), AdminToken: os.Getenv("ADMIN_TOKEN"), WebhookSecret: os.Getenv("WEBHOOK_SECRET"), GitHubToken: os.Getenv("GITHUB_TOKEN"), AAPT: env("AAPT", "aapt"), APKSigner: env("APKSIGNER", "apksigner"), Apps: apps, Federation: federation, ClientAPK: env("CLIENT_APK", "/usr/local/share/store/appstore.apk")})
	if err != nil {
		slog.Error("start", "error", err)
		os.Exit(1)
	}
	defer s.Close()
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	go s.RunNotifications(ctx)
	interval, err := time.ParseDuration(env("POLL_INTERVAL", "15m"))
	if err != nil || interval < 0 {
		slog.Error("invalid POLL_INTERVAL")
		os.Exit(1)
	}
	syncDone := make(chan struct{})
	go func() { defer close(syncDone); s.RunSync(ctx, interval) }()
	srv := &http.Server{Addr: env("LISTEN_ADDR", "127.0.0.1:8080"), Handler: s, BaseContext: func(net.Listener) context.Context { return ctx }, ReadHeaderTimeout: 10 * time.Second, ReadTimeout: 30 * time.Second, WriteTimeout: 10 * time.Minute, IdleTimeout: 60 * time.Second, MaxHeaderBytes: 16 << 10}
	shutdownDone := make(chan struct{})
	go func() {
		defer close(shutdownDone)
		<-ctx.Done()
		c, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		if shutdownErr := srv.Shutdown(c); shutdownErr != nil {
			_ = srv.Close()
		}
		<-syncDone
	}()
	slog.Info("private store listening", "address", srv.Addr)
	cert, key := os.Getenv("TLS_CERT_FILE"), os.Getenv("TLS_KEY_FILE")
	if (cert == "") != (key == "") {
		slog.Error("set both TLS_CERT_FILE and TLS_KEY_FILE")
		os.Exit(1)
	}
	if cert != "" {
		err = srv.ListenAndServeTLS(cert, key)
	} else {
		err = srv.ListenAndServe()
	}
	if err != nil && err != http.ErrServerClosed {
		slog.Error("serve", "error", err)
		os.Exit(1)
	}
	if err == http.ErrServerClosed {
		<-shutdownDone
	}
}
