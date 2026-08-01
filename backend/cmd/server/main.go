package main

import (
	"embed"
	"flag"
	"fmt"
	"io/fs"
	"log"
	"net/http"
	"os"

	"sentence-detective/internal/game"
)

//go:embed web/*
var webFiles embed.FS

func main() {
	defaultAddr := ":" + envOr("PORT", "8080")
	addr := flag.String("addr", envOr("ADDR", defaultAddr), "server address")
	flag.Parse()

	hub := game.NewHub()
	mux := http.NewServeMux()
	hub.RegisterRoutes(mux)

	assets, err := fs.Sub(webFiles, "web")
	if err != nil {
		log.Fatal(err)
	}
	spa := http.FileServer(http.FS(assets))
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/" {
			if _, err := fs.Stat(assets, r.URL.Path[1:]); err != nil {
				r.URL.Path = "/"
			}
		}
		spa.ServeHTTP(w, r)
	})

	server := &http.Server{Addr: *addr, Handler: securityHeaders(mux)}
	fmt.Printf("\n  นักสืบพิชิตประโยคความเดียว พร้อมแล้ว!\n  เปิด http://localhost%s\n\n", *addr)
	log.Fatal(server.ListenAndServe())
}

func envOr(key, fallback string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return fallback
}

func securityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Referrer-Policy", "same-origin")
		if r.Method == http.MethodOptions {
			w.Header().Set("Access-Control-Allow-Origin", "*")
			w.Header().Set("Access-Control-Allow-Headers", "Content-Type")
			w.Header().Set("Access-Control-Allow-Methods", "GET,POST,OPTIONS")
			w.WriteHeader(http.StatusNoContent)
			return
		}
		next.ServeHTTP(w, r)
	})
}
