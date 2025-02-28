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

type UploadRequest struct {
	Image1 string `json:"image1"`
	Image2 string `json:"image2"`
}

var homeHTML = []byte(`<!DOCTYPE html>
<html>
<head>
	<title>Image Display</title>
</head>
<body>
	<h1>Image Display</h1>
	<div id="images" class="images"></div>
	<script>
		const imagesDiv = document.getElementById('images');
		const eventSource = new EventSource('/events');
		eventSource.onmessage = (e) => {
			const data = JSON.parse(e.data);
			imagesDiv.innerHTML = 
				'<img src="' + data.image1 + '">' +
				'<img src="' + data.image2 + '">';
		};
	</script>
</body>
</html>`)

func main() {
	http.HandleFunc("/events", sseHandler)
	http.HandleFunc("/upload", uploadHandler)
	http.HandleFunc("/", homeHandler)

	log.Println("Server running at http://127.0.0.1:8080")
	log.Fatal(http.ListenAndServe(":8080", nil))
}

func uploadHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var req UploadRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Invalid JSON format", http.StatusBadRequest)
		return
	}

	if req.Image1 == "" || req.Image2 == "" {
		http.Error(w, "Both image URLs are required", http.StatusBadRequest)
		return
	}

	msg, _ := json.Marshal(req)
	broadcast(msg)

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	w.Write(msg)
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

	messageChan := make(chan []byte, 1) // Buffered channel

	clientsMu.Lock()
	clients[messageChan] = true
	clientsMu.Unlock()

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
			// Drop message if client buffer is full
		}
	}
}

func homeHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/html")
	w.Write(homeHTML)
}
