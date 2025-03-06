package main

import (
	"database/sql"
	_ "github.com/lib/pq"

	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"strings"
	"sync"

	"github.com/golang-jwt/jwt/v5"
)

var (
	clients   = make(map[chan []byte]bool)
	clientsMu sync.RWMutex
)

type Claims struct {
	UserID uint `json:"user_id"`
	jwt.RegisteredClaims
}

func main() {
	http.HandleFunc("/events", sseHandler)
	http.HandleFunc("/trigger", authMiddleware(triggerHandler))

	log.Println("Server running at http://127.0.0.1:8080")
	log.Fatal(http.ListenAndServe(":8080", nil))
}

func triggerHandler(w http.ResponseWriter, r *http.Request) {
	msg, _ := json.Marshal(map[string]any{"number": 1})
	broadcast(msg)
	w.WriteHeader(http.StatusOK)
	w.Write([]byte("Triggered"))
}

func sseHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("Access-Control-Allow-Origin", "*")

	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "Streaming unsupported", http.StatusInternalServerError)
		return
	}

	messageChan := make(chan []byte, 1)
	clientsMu.Lock()
	clients[messageChan] = true
	clientsMu.Unlock()

	msg, _ := json.Marshal(map[string]any{"number": 1})
	fmt.Fprintf(w, "data: %s\n\n", msg)
	flusher.Flush()

	defer func() {
		clientsMu.Lock()
		delete(clients, messageChan)
		clientsMu.Unlock()
		close(messageChan)
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

func broadcast(msg []byte) {
	clientsMu.RLock()
	defer clientsMu.RUnlock()
	for client := range clients {
		select {
		case client <- msg:
		default:
		}
	}
}

func authMiddleware(next http.HandlerFunc) http.HandlerFunc {
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

		secret := []byte(os.Getenv("JWT_SECRET"))
		if len(secret) == 0 {
			http.Error(w, "Server configuration error", http.StatusInternalServerError)
			return
		}

		claims := &Claims{}
		token, err := jwt.ParseWithClaims(tokenString, claims, func(token *jwt.Token) (any, error) {
			return secret, nil
		})

		if err != nil {
			if err == jwt.ErrTokenMalformed {
				http.Error(w, "Invalid token format", http.StatusUnauthorized)
			} else {
				http.Error(w, "Authentication failed", http.StatusUnauthorized)
			}
			return
		}

		if !token.Valid {
			http.Error(w, "Invalid token", http.StatusUnauthorized)
			return
		}
		fmt.Printf("Full claims: %+v\n", claims)

		if claims.UserID == 0 {
			http.Error(w, "Invalid user claims", http.StatusUnauthorized)
			return
		}

		var connStr = os.Getenv("DATABASE_URL")
		db, err := sql.Open("postgres", connStr)
		if err != nil {
			http.Error(w, "Database error 1", http.StatusInternalServerError)
			return
		}
		defer db.Close()

		var verificationStatus bool
		err = db.QueryRow("SELECT verification_status FROM users WHERE id = $1", claims.UserID).Scan(&verificationStatus)
		if err != nil {
			if err == sql.ErrNoRows {
				http.Error(w, "User not found", http.StatusUnauthorized)
			} else {
				http.Error(w, "Database error 2", http.StatusInternalServerError)
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
