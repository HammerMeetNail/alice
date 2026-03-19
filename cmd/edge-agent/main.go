package main

import (
	"context"
	"encoding/json"
	"flag"
	"log"
	"os"

	"alice/internal/edge"
)

func main() {
	configPath := flag.String("config", "", "path to edge agent JSON config")
	flag.Parse()

	if *configPath == "" {
		log.Fatal("edge agent requires -config")
	}

	cfg, err := edge.LoadConfig(*configPath)
	if err != nil {
		log.Fatalf("load edge config: %v", err)
	}

	report, err := edge.NewRuntime(cfg).RunOnce(context.Background())
	if err != nil {
		log.Fatalf("edge runtime failed: %v", err)
	}

	encoder := json.NewEncoder(os.Stdout)
	encoder.SetIndent("", "  ")
	if err := encoder.Encode(report); err != nil {
		log.Fatalf("encode runtime report: %v", err)
	}
}
