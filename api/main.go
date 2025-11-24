package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	_ "github.com/mattn/go-sqlite3"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"gopkg.in/yaml.v3"
)

// Config represents the application configuration
type Config struct {
	Server struct {
		Domain       string `yaml:"domain"`
		HTTPPort     int    `yaml:"http_port"`
		HTTPSEnabled bool   `yaml:"https_enabled"`
		SSLCert      string `yaml:"ssl_cert"`
		SSLKey       string `yaml:"ssl_key"`
	} `yaml:"server"`
	Streaming struct {
		RTMPPort             int    `yaml:"rtmp_port"`
		HLSPort              int    `yaml:"hls_port"`
		MaxConcurrentStreams int    `yaml:"max_concurrent_streams"`
		DefaultQuality       string `yaml:"default_quality"`
	} `yaml:"streaming"`
	Authentication struct {
		JWTSecret         string `yaml:"jwt_secret"`
		TokenExpiry       string `yaml:"token_expiry"`
		RequireAuth       bool   `yaml:"require_auth"`
		DefaultAdminUser  string `yaml:"default_admin_user"`
		DefaultAdminPass  string `yaml:"default_admin_pass"`
	} `yaml:"authentication"`
	Database struct {
		Type    string `yaml:"type"`
		Path    string `yaml:"path"`
		Backup  bool   `yaml:"backup_enabled"`
	} `yaml:"database"`
	Monitoring struct {
		PrometheusEnabled bool `yaml:"prometheus_enabled"`
		PrometheusPort    int  `yaml:"prometheus_port"`
	} `yaml:"monitoring"`
}

// App represents the application
type App struct {
	Config *Config
	DB     *sql.DB
	Server *http.Server
}

var app *App

func main() {
	log.Println("Starting PutLive API v3.0...")

	// Load configuration
	config, err := loadConfig("/etc/putlive/config.yaml")
	if err != nil {
		log.Fatalf("Failed to load config: %v", err)
	}

	// Initialize database
	db, err := initDatabase(config.Database.Path)
	if err != nil {
		log.Fatalf("Failed to initialize database: %v", err)
	}
	defer db.Close()

	// Initialize app
	app = &App{
		Config: config,
		DB:     db,
	}

	// Create default admin user if not exists
	if err := createDefaultAdmin(db, config); err != nil {
		log.Printf("Warning: Failed to create default admin: %v", err)
	}

	// Initialize metrics
	initMetrics()

	// Setup routes
	mux := setupRoutes()

	// Create HTTP server
	addr := fmt.Sprintf(":%d", config.Server.HTTPPort)
	app.Server = &http.Server{
		Addr:         addr,
		Handler:      mux,
		ReadTimeout:  15 * time.Second,
		WriteTimeout: 15 * time.Second,
		IdleTimeout:  60 * time.Second,
	}

	// Start server in goroutine
	go func() {
		log.Printf("Server listening on %s", addr)
		if config.Server.HTTPSEnabled {
			if err := app.Server.ListenAndServeTLS(config.Server.SSLCert, config.Server.SSLKey); err != nil && err != http.ErrServerClosed {
				log.Fatalf("Server error: %v", err)
			}
		} else {
			if err := app.Server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
				log.Fatalf("Server error: %v", err)
			}
		}
	}()

	// Graceful shutdown
	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit

	log.Println("Shutting down server...")
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	if err := app.Server.Shutdown(ctx); err != nil {
		log.Fatalf("Server forced to shutdown: %v", err)
	}

	log.Println("Server exited")
}

func loadConfig(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}

	var config Config
	if err := yaml.Unmarshal(data, &config); err != nil {
		return nil, err
	}

	return &config, nil
}

func initDatabase(path string) (*sql.DB, error) {
	db, err := sql.Open("sqlite3", path)
	if err != nil {
		return nil, err
	}

	if err := db.Ping(); err != nil {
		return nil, err
	}

	// Create tables if not exist
	schema := `
	CREATE TABLE IF NOT EXISTS users (
		id TEXT PRIMARY KEY,
		username TEXT UNIQUE NOT NULL,
		password_hash TEXT NOT NULL,
		role TEXT NOT NULL,
		created_at DATETIME DEFAULT CURRENT_TIMESTAMP
	);

	CREATE TABLE IF NOT EXISTS stream_keys (
		id TEXT PRIMARY KEY,
		user_id TEXT NOT NULL,
		key_hash TEXT UNIQUE NOT NULL,
		created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
		revoked BOOLEAN DEFAULT 0,
		FOREIGN KEY (user_id) REFERENCES users(id)
	);

	CREATE TABLE IF NOT EXISTS videos (
		id TEXT PRIMARY KEY,
		title TEXT NOT NULL,
		filename TEXT NOT NULL,
		description TEXT,
		duration_seconds INTEGER,
		size_bytes INTEGER,
		status TEXT DEFAULT 'processing',
		created_at DATETIME DEFAULT CURRENT_TIMESTAMP
	);

	CREATE TABLE IF NOT EXISTS schedules (
		id TEXT PRIMARY KEY,
		video_id TEXT NOT NULL,
		start_time DATETIME NOT NULL,
		end_time DATETIME,
		quality TEXT DEFAULT '480p',
		loop BOOLEAN DEFAULT 0,
		status TEXT DEFAULT 'scheduled',
		created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
		FOREIGN KEY (video_id) REFERENCES videos(id)
	);

	CREATE TABLE IF NOT EXISTS analytics (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		stream_name TEXT,
		event_type TEXT,
		viewer_count INTEGER,
		bitrate_kbps INTEGER,
		timestamp DATETIME DEFAULT CURRENT_TIMESTAMP
	);

	CREATE TABLE IF NOT EXISTS sessions (
		id TEXT PRIMARY KEY,
		user_id TEXT NOT NULL,
		token TEXT NOT NULL,
		expires_at DATETIME NOT NULL,
		created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
		FOREIGN KEY (user_id) REFERENCES users(id)
	);
	`

	if _, err := db.Exec(schema); err != nil {
		return nil, err
	}

	return db, nil
}

func setupRoutes() *http.ServeMux {
	mux := http.NewServeMux()

	// Public routes
	mux.HandleFunc("/api/health", handleHealth)
	mux.HandleFunc("/api/health/detailed", authMiddleware(handleDetailedHealth))

	// Auth routes
	mux.HandleFunc("/api/auth/login", handleLogin)
	mux.HandleFunc("/api/auth/logout", authMiddleware(handleLogout))
	mux.HandleFunc("/api/auth/refresh", authMiddleware(handleRefreshToken))

	// Stream routes
	mux.HandleFunc("/api/stream/status", authMiddleware(handleStreamStatus))
	mux.HandleFunc("/api/stream/start", authMiddleware(handleStartStream))
	mux.HandleFunc("/api/stream/stop", authMiddleware(handleStopStream))

	// SRS webhook routes
	mux.HandleFunc("/api/srs/on_publish", handleOnPublish)
	mux.HandleFunc("/api/srs/on_unpublish", handleOnUnpublish)
	mux.HandleFunc("/api/srs/on_play", handleOnPlay)

	// Video routes
	mux.HandleFunc("/api/videos", authMiddleware(handleVideos))
	mux.HandleFunc("/api/videos/upload", authMiddleware(handleVideoUpload))

	// Schedule routes
	mux.HandleFunc("/api/schedule", authMiddleware(handleSchedule))

	// Analytics routes
	mux.HandleFunc("/api/analytics/stream", authMiddleware(handleStreamAnalytics))
	mux.HandleFunc("/api/metrics", authMiddleware(handleMetrics))

	// Prometheus metrics
	if app.Config.Monitoring.PrometheusEnabled {
		mux.Handle("/metrics", promhttp.Handler())
	}

	// CORS middleware
	return corsMiddleware(mux)
}

func corsMiddleware(next *http.ServeMux) *http.ServeMux {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PUT, DELETE, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization")

		if r.Method == "OPTIONS" {
			w.WriteHeader(http.StatusOK)
			return
		}

		next.ServeHTTP(w, r)
	}).(*http.ServeMux)
}

func respondJSON(w http.ResponseWriter, status int, data interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(data)
}

func respondError(w http.ResponseWriter, status int, message string) {
	respondJSON(w, status, map[string]string{"error": message})
}
