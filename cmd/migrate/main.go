// Command migrate creates or updates the MySQL schema used by the ADK
// session service and this project's own llm_usages table (internal/llmusage).
// It runs as an ArgoCD PreSync Job (infra repo:
// manifests/secretary/migration-job.yaml) so the tables exist before the API
// and batch pods start. The CLI mirrors the MH-API/line-bot convention:
//
//	migrate -command up
package main

import (
	"flag"
	"log"

	"github.com/o-ga09/adk-go-sample/internal/config"
	"github.com/o-ga09/adk-go-sample/internal/store"
)

func main() {
	command := flag.String("command", "", "migration command (up)")
	flag.Parse()

	if *command != "up" {
		log.Fatalf("unsupported -command %q (want up)", *command)
	}

	c := config.Load()
	if c.MySQLDSN == "" {
		log.Fatal("MYSQL_DSN is required")
	}

	if err := store.Migrate(c.MySQLDSN); err != nil {
		log.Fatalf("migration failed: %v", err)
	}
	log.Println("migration complete")
}
