package main

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/golang-jwt/jwt/v5"
	_ "github.com/jackc/pgx/v5/stdlib"
)

type Config struct {
	Port        string
	JwtSecret   []byte
	DatabaseURL string
}

type Server struct {
	db        *sql.DB
	config    Config
	clients   map[chan []byte]bool
	clientsMu sync.RWMutex
	logger    *slog.Logger
}

type Claims struct {
	UserID uint `json:"user_id"`
	jwt.RegisteredClaims
}

func main() {
	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))

	cfg := loadConfig()

	db, err := sql.Open("pgx", cfg.DatabaseURL)
	if err != nil {
		logger.Error("Failed to open database connection", "error", err)
		os.Exit(1)
	}
	defer db.Close()

	if err := db.Ping(); err != nil {
		logger.Error("Failed to ping database", "error", err)
		os.Exit(1)
	}

	db.SetMaxOpenConns(25)
	db.SetMaxIdleConns(25)
	db.SetConnMaxLifetime(5 * time.Minute)

	srv := &Server{
		db:      db,
		config:  cfg,
		clients: make(map[chan []byte]bool),
		logger:  logger,
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/events", srv.sseHandler)
	mux.HandleFunc("/trigger", srv.authMiddleware(srv.triggerHandler))

	logger.Info("Server starting", "port", cfg.Port)
	server := &http.Server{
		Addr:    ":" + cfg.Port,
		Handler: mux,
	}

	if err := server.ListenAndServe(); err != nil {
		logger.Error("Server failed", "error", err)
		os.Exit(1)
	}
}

func loadConfig() Config {
	port := os.Getenv("PORT")
	if port == "" {
		port = "8080"
	}

	secret := os.Getenv("JWT_SECRET")
	if secret == "" {
		slog.Warn("JWT_SECRET is not set")
	}

	dbURL := os.Getenv("DATABASE_URL")
	if dbURL == "" {
		slog.Warn("DATABASE_URL is not set")
	}

	return Config{
		Port:        port,
		JwtSecret:   []byte(secret),
		DatabaseURL: dbURL,
	}
}

func (s *Server) triggerHandler(w http.ResponseWriter, r *http.Request) {
	payload := map[string]any{"number": 1, "timestamp": time.Now().Unix()}
	msg, err := json.Marshal(payload)
	if err != nil {
		http.Error(w, "JSON error", http.StatusInternalServerError)
		return
	}

	s.broadcast(msg)

	w.WriteHeader(http.StatusOK)
	w.Write([]byte("Triggered"))
}

func (s *Server) sseHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("Access-Control-Allow-Origin", "*")

	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "Streaming unsupported", http.StatusInternalServerError)
		return
	}

	messageChan := make(chan []byte, 10)

	s.clientsMu.Lock()
	s.clients[messageChan] = true
	s.clientsMu.Unlock()

	s.logger.Info("New SSE client connected")

	initMsg, _ := json.Marshal(map[string]any{"status": "connected"})
	fmt.Fprintf(w, "data: %s\n\n", initMsg)
	flusher.Flush()

	defer func() {
		s.clientsMu.Lock()
		delete(s.clients, messageChan)
		s.clientsMu.Unlock()
		close(messageChan)
		s.logger.Info("SSE client disconnected")
	}()

	for {
		select {
		case msg := <-messageChan:
			fmt.Fprintf(w, "data: %s\n\n", msg)
			flusher.Flush()
		case <-r.Context().Done():
			return
		}
	}
}

func (s *Server) broadcast(msg []byte) {
	s.clientsMu.RLock()
	defer s.clientsMu.RUnlock()

	for clientChan := range s.clients {
		select {
		case clientChan <- msg:
		default:
			s.logger.Warn("Dropping message for slow client")
		}
	}
}

func (s *Server) authMiddleware(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		authHeader := r.Header.Get("Authorization")
		if authHeader == "" {
			http.Error(w, "Authorization header missing", http.StatusUnauthorized)
			return
		}

		parts := strings.Split(authHeader, " ")
		if len(parts) != 2 || strings.ToLower(parts[0]) != "bearer" {
			http.Error(w, "Invalid Authorization header format", http.StatusUnauthorized)
			return
		}
		tokenString := parts[1]

		claims := &Claims{}
		token, err := jwt.ParseWithClaims(tokenString, claims, func(token *jwt.Token) (any, error) {
			return s.config.JwtSecret, nil
		})

		if err != nil || !token.Valid {
			s.logger.Warn("Invalid token attempt", "error", err)
			http.Error(w, "Unauthorized", http.StatusUnauthorized)
			return
		}

		if claims.UserID == 0 {
			http.Error(w, "Invalid user claims", http.StatusUnauthorized)
			return
		}

		var verificationStatus bool
		err = s.db.QueryRow("SELECT verification_status FROM users WHERE id = $1", claims.UserID).Scan(&verificationStatus)

		if err != nil {
			if err == sql.ErrNoRows {
				http.Error(w, "User not found", http.StatusUnauthorized)
			} else {
				s.logger.Error("Database query error", "error", err)
				http.Error(w, "Internal Server Error", http.StatusInternalServerError)
			}
			return
		}

		if verificationStatus {
			http.Error(w, "Already Requested", http.StatusConflict)
			return
		}

		next(w, r)
	}
}
