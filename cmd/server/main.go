package main

import (
	"log"

	"github.com/sanskar/memory-allocator/web"
)

// version is set at build time via -ldflags "-X main.version=...".
// Default is "dev" for local builds.
var version = "dev"

func main() {
	log.Printf("Memory Allocator Simulator %s starting", version)
	cfg := web.ConfigFromEnv()
	srv := web.NewServerWithConfig(cfg)
	if err := srv.Run(); err != nil {
		log.Fatalf("server error: %v", err)
	}
	log.Println("Server stopped cleanly")
}
