// ai-usage-bot keeps one Telegram message up to date with your AI subscription
// limits (Claude Code, Codex), OpenAI API spend and provider status, and warns
// you before a limit runs out.
//
// Usage:
//
//	ai-usage-bot run       run the bot (refresh loop + button handling)
//	ai-usage-bot once      refresh the message one time and exit
//	ai-usage-bot doctor    read every source and print the message; sends nothing
//	ai-usage-bot version
//
// Configuration comes from the environment and an optional .env file; see
// internal/config.
package main

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/voknil/ai-usage-bot/internal/bot"
	"github.com/voknil/ai-usage-bot/internal/claude"
	"github.com/voknil/ai-usage-bot/internal/config"
	"github.com/voknil/ai-usage-bot/internal/openai"
	"github.com/voknil/ai-usage-bot/internal/render"
	"github.com/voknil/ai-usage-bot/internal/statuspage"
	"github.com/voknil/ai-usage-bot/internal/telegram"
	"github.com/voknil/ai-usage-bot/internal/usage"
)

// version is injected at build time via -ldflags "-X main.version=...".
var version = "dev"

const usageText = `usage: ai-usage-bot <run|once|doctor|version>

  run      run the bot: refresh on a schedule and answer the refresh button
  once     refresh the status message one time and exit
  doctor   read every data source and print the message (sends nothing)
  version  print the version
`

func main() {
	if len(os.Args) != 2 {
		fmt.Fprint(os.Stderr, usageText)
		os.Exit(2)
	}
	cmd := os.Args[1]
	if cmd == "version" || cmd == "-version" || cmd == "--version" {
		fmt.Println("ai-usage-bot", version)
		return
	}
	if cmd != "run" && cmd != "once" && cmd != "doctor" {
		fmt.Fprint(os.Stderr, usageText)
		os.Exit(2)
	}

	cfg, err := config.Load()
	if err != nil {
		fmt.Fprintln(os.Stderr, "config:", err)
		os.Exit(2)
	}
	log := newLogger(cfg)
	slog.SetDefault(log)

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	if err := execute(ctx, cmd, cfg, log); err != nil {
		log.Error(cmd+" failed", "err", err)
		os.Exit(1)
	}
}

func execute(ctx context.Context, cmd string, cfg *config.Config, log *slog.Logger) error {
	src := sources(cfg)
	if cmd == "doctor" {
		return doctor(ctx, src)
	}

	b, err := bot.New(telegram.New(cfg.TelegramToken), src, bot.Options{
		ChatID:          cfg.ChatID,
		RefreshInterval: cfg.RefreshInterval,
		CacheTTL:        cfg.CacheTTL,
		Thresholds:      cfg.Thresholds,
		MessagePath:     cfg.MessageStatePath(),
		AlertPath:       cfg.AlertStatePath(),
		OffsetPath:      cfg.OffsetPath(),
	}, log)
	if err != nil {
		return err
	}
	if cmd == "once" {
		return b.Refresh(ctx, true)
	}
	log.Info("starting", "version", version, "refresh", cfg.RefreshInterval.String(), "pinned_chat", cfg.ChatID != 0)
	return b.Run(ctx)
}

// sources wires the providers the configuration enables.
func sources(cfg *config.Config) bot.Sources {
	cl := claude.New(cfg.ClaudeBin, cfg.CredentialsFile)
	src := bot.Sources{
		Claude: cl.Fetch,
	}
	if oa := openai.New(cfg.CodexBarBin, cfg.OpenAIAdminKey); oa.Configured() {
		src.OpenAI = oa.Fetch
	}
	for _, p := range []struct{ name, url string }{
		{"Anthropic", statuspage.AnthropicURL},
		{"OpenAI", statuspage.OpenAIURL},
	} {
		c := statuspage.New(p.name, p.url)
		src.Services = append(src.Services, bot.ServiceSource{Name: p.name, Fetch: c.Fetch})
	}
	return src
}

// doctor reads every source once and prints the outcome plus the message that
// would be sent. It never talks to Telegram.
func doctor(ctx context.Context, src bot.Sources) error {
	var snap render.Snapshot
	failures := 0
	report := func(name string, err error) {
		if err != nil {
			failures++
			fmt.Printf("✗ %-12s %v\n", name, err)
			return
		}
		fmt.Printf("✓ %-12s ok\n", name)
	}

	if src.Claude != nil {
		v, err := src.Claude(ctx)
		report("claude", err)
		if err == nil {
			snap.Claude = v
			fmt.Printf("  source: %s\n", v.Source)
		}
	}
	if src.OpenAI != nil {
		v, err := src.OpenAI(ctx)
		report("openai", err)
		if err == nil {
			snap.OpenAI = v
		}
	} else {
		fmt.Println("- openai       not configured (no codexbar on PATH, no OPENAI_ADMIN_KEY)")
	}
	for _, s := range src.Services {
		v, err := s.Fetch(ctx)
		report("status:"+s.Name, err)
		snap.Services = append(snap.Services, render.ServiceView{Name: s.Name, Svc: svcOrNil(v, err)})
	}

	fmt.Println("\n--- message preview ---")
	fmt.Println(render.Status(snap, time.Now(), time.Local))
	if failures > 0 {
		return fmt.Errorf("%d source(s) failed", failures)
	}
	return nil
}

func svcOrNil(v *usage.Service, err error) *usage.Service {
	if err != nil {
		return nil
	}
	return v
}

func newLogger(cfg *config.Config) *slog.Logger {
	var level slog.Level
	if err := level.UnmarshalText([]byte(cfg.LogLevel)); err != nil {
		level = slog.LevelInfo
	}
	opts := &slog.HandlerOptions{Level: level}
	if cfg.LogFormat == "json" {
		return slog.New(slog.NewJSONHandler(os.Stderr, opts))
	}
	return slog.New(slog.NewTextHandler(os.Stderr, opts))
}
