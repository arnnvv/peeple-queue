package main

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"sync"
	"time"
)

var (
	clients   = make(map[chan []byte]bool)
	clientsMu sync.RWMutex
	counter   = 0
)

func main() {
	generateAndSendNumber()

	go func() {
		ticker := time.NewTicker(1 * time.Minute)
		defer ticker.Stop()
		for range ticker.C {
			counter++
			generateAndSendNumber()
		}
	}()

	http.HandleFunc("/events", sseHandler)
	log.Println("Server running at http://127.0.0.1:8080")
	log.Fatal(http.ListenAndServe(":8080", nil))
}

func generateAndSendNumber() {
	msg, _ := json.Marshal(map[string]any{"number": counter})
	broadcast(msg)
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

	msg, _ := json.Marshal(map[string]any{"number": counter})
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
