package main

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"sync"
)

var (
	clients   = make(map[chan string]bool)
	clientsMu sync.Mutex
)

type UploadRequest struct {
	Image1 string `json:"image1"`
	Image2 string `json:"image2"`
}

func main() {
	http.HandleFunc("/events", sseHandler)
	http.HandleFunc("/upload", uploadHandler)
	http.HandleFunc("/", homeHandler)

	log.Println("Server running at http://127.0.0.1:8080")
	log.Fatal(http.ListenAndServe(":8080", nil))
}

func uploadHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != "POST" {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var req UploadRequest
	err := json.NewDecoder(r.Body).Decode(&req)
	if err != nil {
		http.Error(w, "Invalid JSON format", http.StatusBadRequest)
		return
	}

	if req.Image1 == "" || req.Image2 == "" {
		http.Error(w, "Both image1 and image2 URLs are required", http.StatusBadRequest)
		return
	}

	message := fmt.Sprintf(`{"image1": "%s", "image2": "%s"}`, req.Image1, req.Image2)
	broadcast(message)

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	fmt.Fprint(w, message)
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

	messageChan := make(chan string)
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

func broadcast(msg string) {
	clientsMu.Lock()
	defer clientsMu.Unlock()

	for client := range clients {
		select {
		case client <- msg:
		default:
		}
	}
}

func homeHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/html")
	fmt.Fprint(w, `<!DOCTYPE html>
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
				'<img src="' + data.image2 + '">'
			;
		};
	</script>
</body>
</html>`)
}
