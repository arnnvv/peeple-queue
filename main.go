package main

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"sync"
)

var (
	clients   = make(map[chan []byte]bool)
	clientsMu sync.RWMutex
)

func main() {
	http.HandleFunc("/events", sseHandler)
	http.HandleFunc("/trigger", triggerHandler)

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
