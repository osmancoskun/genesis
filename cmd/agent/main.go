// Command agent runs the path-director client daemon (DNS stub + pin hooks).
package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"os"
	"os/signal"
	"syscall"
	"time"

	"dnsredirector/internal/dnsstub"
	"dnsredirector/internal/pathpin"
	"dnsredirector/internal/rules"
)

func main() {
	rulesPath := flag.String("rules", "configs/example.rules.yaml", "path to rules YAML")
	listen := flag.String("listen", dnsstub.ListenAddr(), "UDP listen address (default 127.0.0.1:5353; Mode A coexistence)")
	pinsMode := flag.String("pins", "auto", "pin backend: auto|dry-run|netlink|noop")
	reaperEvery := flag.Duration("pin-reaper", time.Second, "how often to expire TTL'd pins")
	flag.Parse()

	cfg, err := rules.LoadFile(*rulesPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "load rules: %v\n", err)
		os.Exit(1)
	}

	applier, label, err := pathpin.SelectApplier(*pinsMode)
	if err != nil {
		fmt.Fprintf(os.Stderr, "pins: %v\n", err)
		os.Exit(1)
	}
	pins := pathpin.New(applier)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go pins.RunReaper(ctx, *reaperEvery)

	srv := &dnsstub.Server{
		Addr:   *listen,
		Rules:  cfg,
		Pins:   pins,
		Logger: log.Default(),
	}

	log.Printf("path-director agent: %s", dnsstub.FormatRuleSummary(cfg))
	log.Printf("listen %s (resolved coexistence: docs/resolved-coexistence.md — default Mode A)", *listen)
	log.Printf("pins=%s reaper=%s; iface-down=%s; ipv6=v4-first; doh=warn-only", label, *reaperEvery, cfg.Defaults.OnIfaceDown)

	errCh := make(chan error, 1)
	go func() {
		errCh <- srv.ListenAndServe()
	}()

	sig := make(chan os.Signal, 1)
	signal.Notify(sig, syscall.SIGINT, syscall.SIGTERM)

	select {
	case err := <-errCh:
		if err != nil {
			log.Fatalf("dns stub: %v", err)
		}
	case s := <-sig:
		log.Printf("signal %s, shutting down (%d active pin(s))", s, len(pins.List()))
		cancel()
		_ = srv.Shutdown()
	}
}
