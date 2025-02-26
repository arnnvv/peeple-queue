package main

import (
	"crypto/sha1"
	"encoding/base64"
	"encoding/binary"
	"fmt"
	"io"
	"net"
	"net/http"
)

func wsHandler(w http.ResponseWriter, r *http.Request) {
	if r.Header.Get("Upgrade") != "websocket" ||
		r.Header.Get("Connection") != "Upgrade" {
		http.Error(w, "Not a WebSocket request", 400)
		return
	}

	if r.Header.Get("Sec-WebSocket-Version") != "13" {
		http.Error(w, "Unsupported WebSocket version", 400)
		return
	}

	key := r.Header.Get("Sec-WebSocket-Key")
	hash := sha1.Sum([]byte(key + "258EAFA5-E914-47DA-95CA-C5AB0DC85B11"))
	accept := base64.StdEncoding.EncodeToString(hash[:])

	w.Header().Set("Upgrade", "websocket")
	w.Header().Set("Connection", "Upgrade")
	w.Header().Set("Sec-WebSocket-Accept", accept)
	w.WriteHeader(http.StatusSwitchingProtocols)

	hijacker, ok := w.(http.Hijacker)
	if !ok {
		http.Error(w, "Hijacking not supported", 500)
		return
	}

	conn, _, err := hijacker.Hijack()
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	defer conn.Close()

	for {
		msg, err := readFrame(conn)
		if err != nil {
			if err != io.EOF {
				fmt.Printf("Read error: %v\n", err)
			}
			return
		}

		fmt.Printf("Received: %s\n", msg)

		if err := sendFrame(conn, msg); err != nil {
			fmt.Printf("Write error: %v\n", err)
			return
		}
	}
}

func readFrame(conn net.Conn) (string, error) {
	header := make([]byte, 2)
	_, err := io.ReadFull(conn, header)
	if err != nil {
		return "", err
	}

	fin := header[0] >> 7
	opcode := header[0] & 0x0F
	mask := header[1] >> 7
	payloadLen := int(header[1] & 0x7F)

	switch payloadLen {
	case 126:
		lenBytes := make([]byte, 2)
		_, err = io.ReadFull(conn, lenBytes)
		if err != nil {
			return "", err
		}
		payloadLen = int(binary.BigEndian.Uint16(lenBytes))
	case 127:
		// 64-bit length not implemented for simplicity
		return "", fmt.Errorf("large payloads not supported")
	}

	maskingKey := make([]byte, 4)
	if mask == 1 {
		_, err = io.ReadFull(conn, maskingKey)
		if err != nil {
			return "", err
		}
	}

	// Read payload
	payload := make([]byte, payloadLen)
	_, err = io.ReadFull(conn, payload)
	if err != nil {
		return "", err
	}

	if mask == 1 {
		indices := make([]int, payloadLen)
		for i := range indices {
			payload[i] ^= maskingKey[i%4]
		}
	}

	if opcode != 0x01 || fin != 1 {
		return "", fmt.Errorf("unsupported frame type")
	}

	return string(payload), nil
}

func sendFrame(conn net.Conn, message string) error {
	payload := []byte(message)
	header := []byte{0x81} // FIN + Text frame

	// Simple frame format (no masking for server->client)
	if len(payload) <= 125 {
		header = append(header, byte(len(payload)))
	} else if len(payload) <= 65535 {
		header = append(header, 126)
		lenBytes := make([]byte, 2)
		binary.BigEndian.PutUint16(lenBytes, uint16(len(payload)))
		header = append(header, lenBytes...)
	} else {
		return fmt.Errorf("payload too large")
	}

	_, err := conn.Write(append(header, payload...))
	return err
}

func main() {
	http.HandleFunc("/ws", wsHandler)
	fmt.Println("WebSocket server listening on :8080")
	http.ListenAndServe(":8080", nil)
}
