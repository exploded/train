package main

import (
	"context"
	"database/sql"
	"errors"
	"html/template"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"

	_ "modernc.org/sqlite"

	"train/db"
)

var (
	templates *template.Template
	queries   *db.Queries
	store     *sql.DB

	// Australia/Sydney by default; overridden by APP_TIMEZONE.
	appLocation *time.Location

	// OAuth + session config (populated in main).
	oauthCfg oauthConfig
)

func init() {
	var err error
	templates, err = template.ParseGlob("templates/*.html")
	if err != nil {
		panic("failed to parse templates: " + err.Error())
	}
	if _, err := templates.ParseGlob("templates/partials/*.html"); err != nil {
		panic("failed to parse partial templates: " + err.Error())
	}
}

// rateLimiter — sliding window per-IP. Mirrors moon's pattern.
type rateLimiter struct {
	mu       sync.Mutex
	visitors map[string]*visitor
	rate     int
	window   time.Duration
}

type visitor struct {
	count       int
	windowStart time.Time
}

func newRateLimiter(rate int, window time.Duration) *rateLimiter {
	rl := &rateLimiter{
		visitors: make(map[string]*visitor),
		rate:     rate,
		window:   window,
	}
	go rl.cleanup()
	return rl
}

func (rl *rateLimiter) cleanup() {
	for {
		time.Sleep(time.Minute)
		rl.mu.Lock()
		for ip, v := range rl.visitors {
			if time.Since(v.windowStart) > rl.window {
				delete(rl.visitors, ip)
			}
		}
		rl.mu.Unlock()
	}
}

func (rl *rateLimiter) allow(ip string) bool {
	rl.mu.Lock()
	defer rl.mu.Unlock()
	v, ok := rl.visitors[ip]
	now := time.Now()
	if !ok || now.Sub(v.windowStart) > rl.window {
		rl.visitors[ip] = &visitor{count: 1, windowStart: now}
		return true
	}
	v.count++
	return v.count <= rl.rate
}

func rateLimit(limiter *rateLimiter, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ip, _, err := net.SplitHostPort(r.RemoteAddr)
		if err != nil {
			ip = r.RemoteAddr
		}
		if !limiter.allow(ip) {
			slog.Warn("rate limit exceeded", "ip", ip)
			http.Error(w, "Too Many Requests", http.StatusTooManyRequests)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func requestLogger(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		next.ServeHTTP(w, r)
		slog.Info("request", "method", r.Method, "uri", r.RequestURI, "duration", time.Since(start))
	})
}

func securityHeaders(isProd bool, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("X-Frame-Options", "DENY")
		w.Header().Set("Referrer-Policy", "strict-origin-when-cross-origin")
		if isProd {
			w.Header().Set("Strict-Transport-Security", "max-age=63072000; includeSubDomains")
		}
		// Tight CSP: only self for everything. HTMX is vendored, no inline
		// scripts, no third-party origins. Inline styles allowed because the
		// barbell SVG sets fill via the style attribute.
		w.Header().Set("Content-Security-Policy",
			"default-src 'self'; script-src 'self'; style-src 'self' 'unsafe-inline'; img-src 'self' data:; connect-src 'self'; frame-ancestors 'none'")
		next.ServeHTTP(w, r)
	})
}

func cacheStaticAssets(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "public, max-age=604800, immutable")
		next.ServeHTTP(w, r)
	})
}

var limiter = newRateLimiter(120, time.Minute)

func makeServerFromMux(mux *http.ServeMux, isProd bool) *http.Server {
	return &http.Server{
		ReadTimeout:  5 * time.Second,
		WriteTimeout: 10 * time.Second,
		IdleTimeout:  60 * time.Second,
		Handler:      requestLogger(rateLimit(limiter, securityHeaders(isProd, mux))),
	}
}

func makeHTTPServer(isProd bool) *http.Server {
	mux := &http.ServeMux{}

	// Public.
	mux.HandleFunc("GET /login", handleLoginPage)
	mux.HandleFunc("GET /privacy", handlePrivacyPage)
	mux.HandleFunc("GET /terms", handleTermsPage)
	mux.HandleFunc("GET /contact", handleContactPage)
	mux.HandleFunc("POST /contact", handleContactSubmit)
	mux.HandleFunc("GET /auth/login", handleAuthLogin)
	mux.HandleFunc("GET /auth/google/callback", handleAuthCallback)
	mux.HandleFunc("POST /auth/logout", handleAuthLogout)

	// Authenticated.
	mux.Handle("GET /{$}", requireAuth(http.HandlerFunc(handleHome)))
	mux.Handle("GET /workout", requireAuth(http.HandlerFunc(handleWorkout)))
	mux.Handle("POST /workout/{id}/finish", requireAuth(http.HandlerFunc(handleWorkoutFinish)))
	mux.Handle("POST /workout/{id}/unfinish", requireAuth(http.HandlerFunc(handleWorkoutUnfinish)))
	mux.Handle("GET /workout/{id}/expand", requireAuth(http.HandlerFunc(handleWorkoutExpand)))
	mux.Handle("GET /history", requireAuth(http.HandlerFunc(handleHistoryPage)))
	mux.Handle("GET /history/more", requireAuth(http.HandlerFunc(handleHistoryMore)))
	mux.Handle("POST /settings/theme", requireAuth(http.HandlerFunc(handleSetTheme)))
	mux.Handle("GET /charts", requireAuth(http.HandlerFunc(handleCharts)))
	mux.Handle("GET /settings", requireAuth(http.HandlerFunc(handleSettings)))
	mux.Handle("POST /settings/exercise/{id}/toggle", requireAuth(http.HandlerFunc(handleSettingsToggle)))
	mux.Handle("POST /settings/reorder", requireAuth(http.HandlerFunc(handleSettingsReorder)))
	mux.Handle("POST /sets/{id}/tap", requireAuth(http.HandlerFunc(handleSetTap)))
	mux.Handle("POST /exercise/{id}/weight", requireAuth(http.HandlerFunc(handleWeightChange)))
	mux.Handle("POST /exercise/{id}/walking-done", requireAuth(http.HandlerFunc(handleWalkingDone)))
	mux.Handle("POST /exercise/{id}/walking-adjust", requireAuth(http.HandlerFunc(handleWalkingAdjust)))

	// Static.
	cwd, _ := os.Getwd()
	fileServer := http.FileServer(http.Dir(cwd + "/static"))
	mux.Handle("GET /static/", http.StripPrefix("/static/", cacheStaticAssets(fileServer)))

	// Browsers auto-request /favicon.ico; serve the SVG directly so it
	// doesn't 404 and doesn't require the link tag to be present.
	mux.HandleFunc("GET /favicon.ico", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "image/svg+xml")
		w.Header().Set("Cache-Control", "public, max-age=604800, immutable")
		http.ServeFile(w, r, cwd+"/static/favicon.svg")
	})

	mux.HandleFunc("/", handle404)

	return makeServerFromMux(mux, isProd)
}

func handle404(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(http.StatusNotFound)
	data := struct{ ThemeMode string }{ThemeMode: themeFromRequest(r)}
	if err := templates.ExecuteTemplate(w, "404.html", data); err != nil {
		slog.Error("404 template", "error", err)
	}
}

func main() {
	slog.SetDefault(slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelInfo})))

	flgProduction := os.Getenv("PROD") == "True"

	port := os.Getenv("PORT")
	if port == "" {
		port = "8080"
	}

	tzName := os.Getenv("APP_TIMEZONE")
	if tzName == "" {
		tzName = "Australia/Sydney"
	}
	loc, err := time.LoadLocation(tzName)
	if err != nil {
		slog.Error("invalid timezone", "tz", tzName, "error", err, "fallback", "UTC")
		loc = time.UTC
	}
	appLocation = loc

	dbPath := os.Getenv("DB_PATH")
	if dbPath == "" {
		dbPath = "train.db"
	}
	store, err = sql.Open("sqlite", dbPath+"?_pragma=journal_mode(WAL)&_pragma=busy_timeout(5000)&_pragma=foreign_keys(on)")
	if err != nil {
		slog.Error("open sqlite", "error", err)
		os.Exit(1)
	}
	defer store.Close()

	if err := db.Migrate(store); err != nil {
		slog.Error("migrate", "error", err)
		os.Exit(1)
	}
	queries = db.New(store)

	if err := db.SeedExercises(context.Background(), queries); err != nil {
		slog.Error("seed exercises", "error", err)
		os.Exit(1)
	}

	if err := initOAuth(context.Background()); err != nil {
		slog.Warn("oauth init", "error", err, "impact", "/auth/login will fail until env is fixed")
	}

	slog.Info("starting", "prod", flgProduction, "port", port, "tz", appLocation.String(), "db", dbPath)

	srv := makeHTTPServer(flgProduction)
	srv.Addr = ":" + port

	go func() {
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			slog.Error("listen and serve", "error", err)
			os.Exit(1)
		}
	}()

	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit
	slog.Info("shutting down")

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := srv.Shutdown(ctx); err != nil {
		slog.Error("shutdown", "error", err)
	}
}
