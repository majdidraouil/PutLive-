package main

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"strings"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"golang.org/x/crypto/bcrypt"
)

type contextKey string

const userContextKey contextKey = "user"

type Claims struct {
	UserID   string `json:"user_id"`
	Username string `json:"username"`
	Role     string `json:"role"`
	jwt.RegisteredClaims
}

type User struct {
	ID           string    `json:"id"`
	Username     string    `json:"username"`
	PasswordHash string    `json:"-"`
	Role         string    `json:"role"`
	CreatedAt    time.Time `json:"created_at"`
}

type LoginRequest struct {
	Username string `json:"username"`
	Password string `json:"password"`
}

type LoginResponse struct {
	Token     string `json:"token"`
	ExpiresIn int64  `json:"expires_in"`
	User      User   `json:"user"`
}

func createDefaultAdmin(db *sql.DB, config *Config) error {
	// Check if admin exists
	var count int
	err := db.QueryRow("SELECT COUNT(*) FROM users WHERE username = ?", config.Authentication.DefaultAdminUser).Scan(&count)
	if err != nil {
		return err
	}

	if count > 0 {
		return nil // Admin already exists
	}

	// Hash password
	hash, err := bcrypt.GenerateFromPassword([]byte(config.Authentication.DefaultAdminPass), bcrypt.DefaultCost)
	if err != nil {
		return err
	}

	// Generate UUID
	id := generateUUID()

	// Insert admin user
	_, err = db.Exec(
		"INSERT INTO users (id, username, password_hash, role) VALUES (?, ?, ?, ?)",
		id, config.Authentication.DefaultAdminUser, string(hash), "admin",
	)

	return err
}

func handleLogin(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		respondError(w, http.StatusMethodNotAllowed, "Method not allowed")
		return
	}

	var req LoginRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		respondError(w, http.StatusBadRequest, "Invalid request body")
		return
	}

	// Validate input
	if req.Username == "" || req.Password == "" {
		respondError(w, http.StatusBadRequest, "Username and password required")
		return
	}

	// Get user from database
	var user User
	err := app.DB.QueryRow(
		"SELECT id, username, password_hash, role, created_at FROM users WHERE username = ?",
		req.Username,
	).Scan(&user.ID, &user.Username, &user.PasswordHash, &user.Role, &user.CreatedAt)

	if err == sql.ErrNoRows {
		respondError(w, http.StatusUnauthorized, "Invalid credentials")
		return
	}
	if err != nil {
		respondError(w, http.StatusInternalServerError, "Database error")
		return
	}

	// Verify password
	if err := bcrypt.CompareHashAndPassword([]byte(user.PasswordHash), []byte(req.Password)); err != nil {
		respondError(w, http.StatusUnauthorized, "Invalid credentials")
		return
	}

	// Generate JWT token
	expiryDuration := 24 * time.Hour
	expiryTime := time.Now().Add(expiryDuration)

	claims := &Claims{
		UserID:   user.ID,
		Username: user.Username,
		Role:     user.Role,
		RegisteredClaims: jwt.RegisteredClaims{
			ExpiresAt: jwt.NewNumericDate(expiryTime),
			IssuedAt:  jwt.NewNumericDate(time.Now()),
			Issuer:    "putlive",
		},
	}

	token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	tokenString, err := token.SignedString([]byte(app.Config.Authentication.JWTSecret))
	if err != nil {
		respondError(w, http.StatusInternalServerError, "Failed to generate token")
		return
	}

	// Store session in database
	sessionID := generateUUID()
	_, err = app.DB.Exec(
		"INSERT INTO sessions (id, user_id, token, expires_at) VALUES (?, ?, ?, ?)",
		sessionID, user.ID, tokenString, expiryTime,
	)
	if err != nil {
		respondError(w, http.StatusInternalServerError, "Failed to create session")
		return
	}

	// Return response
	respondJSON(w, http.StatusOK, LoginResponse{
		Token:     tokenString,
		ExpiresIn: int64(expiryDuration.Seconds()),
		User:      user,
	})
}

func handleLogout(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		respondError(w, http.StatusMethodNotAllowed, "Method not allowed")
		return
	}

	user := r.Context().Value(userContextKey).(*User)

	// Delete session
	_, err := app.DB.Exec("DELETE FROM sessions WHERE user_id = ?", user.ID)
	if err != nil {
		respondError(w, http.StatusInternalServerError, "Failed to logout")
		return
	}

	respondJSON(w, http.StatusOK, map[string]string{"message": "Logged out successfully"})
}

func handleRefreshToken(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		respondError(w, http.StatusMethodNotAllowed, "Method not allowed")
		return
	}

	user := r.Context().Value(userContextKey).(*User)

	// Generate new token
	expiryDuration := 24 * time.Hour
	expiryTime := time.Now().Add(expiryDuration)

	claims := &Claims{
		UserID:   user.ID,
		Username: user.Username,
		Role:     user.Role,
		RegisteredClaims: jwt.RegisteredClaims{
			ExpiresAt: jwt.NewNumericDate(expiryTime),
			IssuedAt:  jwt.NewNumericDate(time.Now()),
			Issuer:    "putlive",
		},
	}

	token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	tokenString, err := token.SignedString([]byte(app.Config.Authentication.JWTSecret))
	if err != nil {
		respondError(w, http.StatusInternalServerError, "Failed to generate token")
		return
	}

	respondJSON(w, http.StatusOK, map[string]interface{}{
		"token":      tokenString,
		"expires_in": int64(expiryDuration.Seconds()),
	})
}

func authMiddleware(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		// Skip auth if not required
		if !app.Config.Authentication.RequireAuth {
			next(w, r)
			return
		}

		// Extract token from Authorization header
		authHeader := r.Header.Get("Authorization")
		if authHeader == "" {
			respondError(w, http.StatusUnauthorized, "Authorization header required")
			return
		}

		parts := strings.Split(authHeader, " ")
		if len(parts) != 2 || parts[0] != "Bearer" {
			respondError(w, http.StatusUnauthorized, "Invalid authorization header format")
			return
		}

		tokenString := parts[1]

		// Parse and validate token
		claims := &Claims{}
		token, err := jwt.ParseWithClaims(tokenString, claims, func(token *jwt.Token) (interface{}, error) {
			return []byte(app.Config.Authentication.JWTSecret), nil
		})

		if err != nil || !token.Valid {
			respondError(w, http.StatusUnauthorized, "Invalid token")
			return
		}

		// Check if session exists
		var sessionID string
		err = app.DB.QueryRow(
			"SELECT id FROM sessions WHERE user_id = ? AND token = ? AND expires_at > ?",
			claims.UserID, tokenString, time.Now(),
		).Scan(&sessionID)

		if err == sql.ErrNoRows {
			respondError(w, http.StatusUnauthorized, "Session expired")
			return
		}
		if err != nil {
			respondError(w, http.StatusInternalServerError, "Database error")
			return
		}

		// Get user from database
		var user User
		err = app.DB.QueryRow(
			"SELECT id, username, role, created_at FROM users WHERE id = ?",
			claims.UserID,
		).Scan(&user.ID, &user.Username, &user.Role, &user.CreatedAt)

		if err != nil {
			respondError(w, http.StatusUnauthorized, "User not found")
			return
		}

		// Add user to context
		ctx := context.WithValue(r.Context(), userContextKey, &user)
		next(w, r.WithContext(ctx))
	}
}

func generateUUID() string {
	b := make([]byte, 16)
	rand.Read(b)
	return hex.EncodeToString(b)
}
