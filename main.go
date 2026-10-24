package main

import (
	"context"
	"embed"
	"encoding/json"
	"fmt"
	"html/template"
	"io"
	"io/fs"
	"log"
	"net/http"
	"net/url"
	"os"
	"path"
	"strings"
	"time"
)

//go:embed templates/*.html static/*
var assets embed.FS

type App struct {
	config    TelemetryConfig
	database  *url.URL
	authToken string
	templates *template.Template
	client    *http.Client
}

type dashboardData struct {
	Points  template.JS
	Latest  *TelemetryPoint
	Updated string
	Error   string
}

func main() {
	if err := loadDotEnv(".env"); err != nil {
		log.Fatalf("could not read .env: %v", err)
	}

	app, err := newApp()
	if err != nil {
		log.Fatal(err)
	}

	staticFS, err := fs.Sub(assets, "static")
	if err != nil {
		log.Fatal(err)
	}
	mux := http.NewServeMux()
	mux.Handle("/static/", http.StripPrefix("/static/", http.FileServer(http.FS(staticFS))))
	mux.HandleFunc("/dashboard", app.fragment)
	mux.HandleFunc("/", app.dashboard)

	server := &http.Server{
		Addr:              ":" + env("PORT", "8080"),
		Handler:           securityHeaders(mux),
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       10 * time.Second,
		WriteTimeout:      20 * time.Second,
		IdleTimeout:       60 * time.Second,
	}
	log.Printf("dashboard listening on %s", server.Addr)
	log.Fatal(server.ListenAndServe())
}

func newApp() (*App, error) {
	database, err := url.Parse(strings.TrimSpace(os.Getenv("FIREBASE_DATABASE_URL")))
	if err != nil || database.Host == "" || (database.Scheme != "https" && database.Scheme != "http") {
		return nil, fmt.Errorf("FIREBASE_DATABASE_URL must be a valid HTTP or HTTPS Realtime Database URL")
	}
	tmpl, err := template.New("").Funcs(template.FuncMap{
		"deref": func(value *float64) float64 {
			if value == nil {
				return 0
			}
			return *value
		},
	}).ParseFS(assets, "templates/*.html")
	if err != nil {
		return nil, fmt.Errorf("parse dashboard templates: %w", err)
	}
	return &App{
		config: TelemetryConfig{
			SourcePath:       env("TELEMETRY_SOURCE_PATH", "soilMoisture"),
			TimestampField:   env("TELEMETRY_TIMESTAMP_FIELD", "timestamp"),
			ValueField:       env("TELEMETRY_VALUE_FIELD", "value"),
			TemperatureField: env("TELEMETRY_TEMPERATURE_FIELD", "tempC"),
		},
		database:  database,
		authToken: os.Getenv("FIREBASE_AUTH_TOKEN"),
		templates: tmpl,
		client:    &http.Client{Timeout: 10 * time.Second},
	}, nil
}

func (a *App) dashboard(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/" {
		http.NotFound(w, r)
		return
	}
	if r.Method != http.MethodGet {
		w.Header().Set("Allow", http.MethodGet)
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	a.render(w, "page", r.Context())
}

func (a *App) fragment(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		w.Header().Set("Allow", http.MethodGet)
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	a.render(w, "dashboard", r.Context())
}

func (a *App) render(w http.ResponseWriter, name string, ctx context.Context) {
	allPoints, err := a.readTelemetry(ctx)
	data := dashboardData{
		Points:  template.JS("[]"),
		Updated: time.Now().Format("Jan 2, 2006 · 3:04 PM MST"),
	}
	if err != nil {
		// Keep credentials and Firebase URLs out of server logs.
		log.Printf("telemetry read failed for configured source path %q", a.config.SourcePath)
		data.Error = "Live readings are temporarily unavailable. The monitor will retry automatically."
	} else {
		points := filterRecent(allPoints, time.Now().UTC())
		encoded, marshalErr := json.Marshal(points)
		if marshalErr == nil {
			// encoding/json escapes HTML-sensitive characters before the JSON is
			// placed in a non-executable application/json script element.
			data.Points = template.JS(encoded)
		}
		if latest, ok := latestByTimestamp(allPoints); ok {
			data.Latest = &latest
		}
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	if err := a.templates.ExecuteTemplate(w, name, data); err != nil {
		log.Printf("render dashboard: %v", err)
	}
}

func (a *App) readTelemetry(ctx context.Context) ([]TelemetryPoint, error) {
	raw, err := a.readPath(ctx, a.config.SourcePath)
	if err != nil {
		return nil, err
	}
	return ParseTelemetry(raw, a.config)
}

func (a *App) readPath(ctx context.Context, firebasePath string) ([]byte, error) {
	clean := path.Clean("/" + strings.TrimSpace(firebasePath))
	if clean == "/" || clean == "/." || strings.Contains(clean, "..") {
		return nil, fmt.Errorf("invalid Firebase source path")
	}
	u := *a.database
	u.Path = strings.TrimSuffix(u.Path, "/") + clean + ".json"
	query := u.Query()
	if a.authToken != "" {
		query.Set("auth", a.authToken)
	}
	u.RawQuery = query.Encode()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return nil, fmt.Errorf("create Firebase request")
	}
	resp, err := a.client.Do(req)
	if err != nil {
		// Do not wrap the underlying url.Error: its text can contain the auth query.
		return nil, fmt.Errorf("Firebase request failed")
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("Firebase returned %s", resp.Status)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 2<<20))
	if err != nil {
		return nil, fmt.Errorf("read Firebase response")
	}
	return body, nil
}

func env(name, fallback string) string {
	if value := os.Getenv(name); value != "" {
		return value
	}
	return fallback
}

func loadDotEnv(filename string) error {
	file, err := os.Open(filename)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	defer file.Close()
	return parseEnv(file, os.Setenv, os.LookupEnv)
}

func securityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Security-Policy", "default-src 'self'; script-src 'self'; style-src 'self'; connect-src 'self'; img-src 'self' data:; base-uri 'none'; frame-ancestors 'none'; object-src 'none'")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Referrer-Policy", "no-referrer")
		w.Header().Set("X-Frame-Options", "DENY")
		next.ServeHTTP(w, r)
	})
}
