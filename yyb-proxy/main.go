package main

import (
	"io"
	"log"
	"net"
	"net/http"
	"os"
)

func main() {
	port := os.Getenv("PORT")
	if port == "" {
		port = "1080"
	}
	authUser := os.Getenv("PROXY_USER")
	authPass := os.Getenv("PROXY_PASS")

	handler := &proxyHandler{user: authUser, pass: authPass}
	log.Printf("yyb-proxy listening on :%s", port)
	log.Fatal(http.ListenAndServe(":"+port, handler))
}

type proxyHandler struct{ user, pass string }

func (h *proxyHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if h.user != "" {
		u, p, ok := r.BasicAuth()
		if !ok || u != h.user || p != h.pass {
			w.Header().Set("Proxy-Authenticate", "Basic")
			http.Error(w, "proxy auth required", http.StatusProxyAuthRequired)
			return
		}
	}

	if r.Method == http.MethodConnect {
		h.handleConnect(w, r)
		return
	}

	// Plain HTTP forward
	resp, err := http.DefaultTransport.RoundTrip(r)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadGateway)
		return
	}
	defer resp.Body.Close()
	for k, vs := range resp.Header {
		for _, v := range vs {
			w.Header().Add(k, v)
		}
	}
	w.WriteHeader(resp.StatusCode)
	io.Copy(w, resp.Body)
}

func (h *proxyHandler) handleConnect(w http.ResponseWriter, r *http.Request) {
	destConn, err := net.Dial("tcp", r.Host)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadGateway)
		return
	}

	hijacker, ok := w.(http.Hijacker)
	if !ok {
		http.Error(w, "hijack not supported", http.StatusInternalServerError)
		return
	}
	clientConn, _, err := hijacker.Hijack()
	if err != nil {
		destConn.Close()
		return
	}
	clientConn.Write([]byte("HTTP/1.1 200 Connection Established\r\n\r\n"))

	go func() { io.Copy(destConn, clientConn); destConn.Close() }()
	go func() { io.Copy(clientConn, destConn); clientConn.Close() }()
}
