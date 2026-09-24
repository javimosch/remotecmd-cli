package main

import (
	"crypto/tls"
	"log"
	"net/http"
)

// startRelayTLS starts the relay with TLS encryption.
// certFile and keyFile are paths to PEM-encoded certificate and key files.
func startRelayTLS(host string, port int, certFile, keyFile string) {
	rs := newRelayFromEnv()
	rs.port = port
	go listenForRestartSignal(nil)

	addr := relayListenAddr(host, port)
	log.Printf("Relay listening on %s (TLS)", addr)

	server := &http.Server{
		Addr:      addr,
		Handler:   rs.mux(),
		TLSConfig: &tls.Config{MinVersion: tls.VersionTLS12},
	}

	if err := server.ListenAndServeTLS(certFile, keyFile); err != nil {
		log.Fatalf("Relay TLS failed: %v", err)
	}
}
