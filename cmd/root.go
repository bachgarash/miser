package cmd

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"strconv"
	"syscall"

	"github.com/spf13/cobra"

	"miser/internal/config"
	"miser/internal/proxy"
	"miser/internal/tracker"
	"miser/internal/tui"
)

var (
	cfgPath  string
	port     int
	target   string
	headless bool
)

var rootCmd = &cobra.Command{
	Use:   "miser",
	Short: "Anthropic API proxy with real-time cost tracking",
	Long: `Miser is a local proxy that sits between your coding tool (Cursor, Windsurf, …)
and the Anthropic API. It transparently forwards every request while tracking
token usage and cost in a k9s-style terminal dashboard.

Point your tool at http://localhost:8080 instead of api.anthropic.com and
watch your spend in real time.`,
	Example: `  miser                            Run proxy + TUI dashboard
  miser --port 9090                Use a custom port
  miser --headless                 Run proxy only (no TUI, logs to stderr)
  miser -c ~/.config/miser/my.toml Use a specific config file
  MISER_PORT=9090 miser            Configure via environment`,
	SilenceUsage:  true,
	SilenceErrors: true,
	RunE:          runServe,
}

func Execute() error {
	return rootCmd.Execute()
}

func init() {
	rootCmd.PersistentFlags().StringVarP(&cfgPath, "config", "c", "",
		"config file path [$MISER_CONFIG]")

	rootCmd.Flags().IntVarP(&port, "port", "p", 0,
		"proxy listen port [$MISER_PORT]")
	rootCmd.Flags().StringVarP(&target, "target", "t", "",
		"upstream API base URL [$MISER_TARGET]")
	rootCmd.Flags().BoolVar(&headless, "headless", false,
		"run proxy without TUI (daemon / CI mode)")
}

func runServe(cmd *cobra.Command, _ []string) error {
	cfg, err := resolveConfig(cmd)
	if err != nil {
		return err
	}
	applyPricing(cfg)

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	t := tracker.New()

	if headless {
		t.OnRecord = func(r tracker.Request) {
			status := fmt.Sprintf("%d", r.StatusCode)
			if r.Error != "" {
				status = "ERR"
			}
			fmt.Fprintf(os.Stderr, "%s  %-22s  %6s in  %6s out  %8s  %6s  %s\n",
				r.Timestamp.Format("15:04:05"),
				r.Model,
				fmtTok(r.InputTokens), fmtTok(r.OutputTokens),
				fmtCost(r.Cost),
				fmtLat(r.Latency),
				status,
			)
		}
	}

	srv := proxy.NewServer(cfg.Proxy.Port, cfg.Proxy.Target, cfg.ProxyTimeout(), t)

	errCh := make(chan error, 1)
	go func() { errCh <- srv.Start(ctx) }()

	if headless {
		fmt.Fprintf(os.Stderr, "miser proxy listening on :%d → %s (ctrl-c to stop)\n",
			cfg.Proxy.Port, cfg.Proxy.Target)
		select {
		case err := <-errCh:
			return err
		case <-ctx.Done():
			return nil
		}
	}

	proxyAddr := fmt.Sprintf("localhost:%d", cfg.Proxy.Port)
	app := tui.New(t, proxyAddr, cfg.Proxy.Target)
	return app.Run()
}

// resolveConfig merges: defaults → config file → env vars → CLI flags.
func resolveConfig(cmd *cobra.Command) (config.Config, error) {
	path := cfgPath
	if path == "" {
		path = os.Getenv("MISER_CONFIG")
	}

	cfg, err := config.Load(path)
	if err != nil {
		return cfg, err
	}

	if v := os.Getenv("MISER_PORT"); v != "" && !cmd.Flags().Changed("port") {
		if p, err := strconv.Atoi(v); err == nil {
			cfg.Proxy.Port = p
		}
	}
	if v := os.Getenv("MISER_TARGET"); v != "" && !cmd.Flags().Changed("target") {
		cfg.Proxy.Target = v
	}

	if cmd.Flags().Changed("port") {
		cfg.Proxy.Port = port
	}
	if cmd.Flags().Changed("target") {
		cfg.Proxy.Target = target
	}

	return cfg, nil
}

func applyPricing(cfg config.Config) {
	if len(cfg.Models) == 0 && cfg.Fallback == nil {
		return
	}

	var models map[string]tracker.ModelPricingEntry
	if len(cfg.Models) > 0 {
		models = make(map[string]tracker.ModelPricingEntry, len(cfg.Models))
		for name, mc := range cfg.Models {
			models[name] = tracker.ModelPricingEntry{
				Aliases:           mc.Aliases,
				InputPerMTok:      mc.InputPerMTok,
				OutputPerMTok:     mc.OutputPerMTok,
				CacheReadPerMTok:  mc.CacheReadPerMTok,
				CacheWritePerMTok: mc.CacheWritePerMTok,
			}
		}
	}

	var fb *tracker.Pricing
	if cfg.Fallback != nil {
		fb = &tracker.Pricing{
			InputPerMTok:      cfg.Fallback.InputPerMTok,
			OutputPerMTok:     cfg.Fallback.OutputPerMTok,
			CacheReadPerMTok:  cfg.Fallback.CacheReadPerMTok,
			CacheWritePerMTok: cfg.Fallback.CacheWritePerMTok,
		}
	}

	tracker.ApplyPricing(models, fb)
}

// compact formatters for headless log line
func fmtTok(n int) string {
	switch {
	case n >= 1_000_000:
		return fmt.Sprintf("%.1fM", float64(n)/1_000_000)
	case n >= 1_000:
		return fmt.Sprintf("%.1fK", float64(n)/1_000)
	default:
		return fmt.Sprintf("%d", n)
	}
}

func fmtCost(c float64) string {
	if c == 0 {
		return "$0.00"
	}
	return fmt.Sprintf("$%.4f", c)
}

func fmtLat(d interface{ Seconds() float64 }) string {
	return fmt.Sprintf("%.1fs", d.Seconds())
}
