package main

import (
	"crypto/tls"
	"fmt"
	"log"
	"net/http"
)

// startRelayTLS starts the relay with TLS encryption.
// certFile and keyFile are paths to PEM-encoded certificate and key files.
func startRelayTLS(port int, certFile, keyFile string) {
	rs := &RelayServer{
		port:          port,
		clients:       make(map[string]*relayClient),
		pending:       make(map[string]*pendingRequest),
		pairListeners: make(map[string]*relayClient),
		multiPending:  make(map[string]*multiPendingEntry),
		subToMulti:    make(map[string]*subTargetInfo),
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"status":"healthy"}`))
	})
	mux.HandleFunc("/", rs.handleWS)

	addr := fmt.Sprintf(":%d", port)
	log.Printf("Relay listening on %s (TLS)", addr)

	server := &http.Server{
		Addr:      addr,
		Handler:   mux,
		TLSConfig: &tls.Config{MinVersion: tls.VersionTLS12},
	}

	if err := server.ListenAndServeTLS(certFile, keyFile); err != nil {
		log.Fatalf("Relay TLS failed: %v", err)
	}
}
