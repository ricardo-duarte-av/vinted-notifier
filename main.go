// Command vinted-notifier is a long-lived daemon that watches Vinted searches
// and posts new listings and price changes into a Matrix room. Searches are
// managed at runtime with !vinted commands in that room.
package main

import (
	"context"
	"flag"
	"log"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/ricardo-duarte-av/vinted-notifier/internal/config"
	"github.com/ricardo-duarte-av/vinted-notifier/internal/matrix"
	"github.com/ricardo-duarte-av/vinted-notifier/internal/poller"
	"github.com/ricardo-duarte-av/vinted-notifier/internal/store"
	"github.com/ricardo-duarte-av/vinted-notifier/internal/vinted"
)

func main() {
	configPath := flag.String("config", "config.yaml", "path to config file")
	flag.Parse()

	cfg, err := config.Load(*configPath)
	if err != nil {
		log.Fatalf("config: %v", err)
	}

	st, err := store.Open(cfg.DBPath)
	if err != nil {
		log.Fatalf("store: %v", err)
	}
	defer st.Close()

	// One client for the poller and chat commands, so they share a pacer.
	vc := vinted.NewClient(cfg.Vinted.Domain)

	bot, err := matrix.New(cfg, st, vc)
	if err != nil {
		log.Fatalf("matrix: %v", err)
	}

	p := poller.New(st, vc, bot,
		time.Duration(cfg.Poll.IntervalSeconds)*time.Second, cfg.Poll.Jitter(), cfg.Poll.MaxPages,
		cfg.Vinted.AllowedCountries)
	bot.SetSeeder(p)

	// Cancel everything on SIGINT/SIGTERM.
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	go p.Run(ctx)

	countries := "all"
	if len(cfg.Vinted.AllowedCountries) > 0 {
		countries = strings.Join(cfg.Vinted.AllowedCountries, ",")
	}
	log.Printf("vinted-notifier started (site=%s, seller countries=%s, interval=%ds ±%.0f%%, db=%s)",
		cfg.Vinted.Domain, countries, cfg.Poll.IntervalSeconds, cfg.Poll.Jitter()*100, cfg.DBPath)
	if err := bot.Run(ctx); err != nil && ctx.Err() == nil {
		log.Fatalf("matrix sync: %v", err)
	}
	log.Println("shutting down")
}
