package tui

import (
	"encoding/csv"
	"fmt"
	"os"
	"strconv"
	"time"

	"github.com/gdamore/tcell/v2"
	"github.com/rivo/tview"

	"miser/internal/tracker"
)

const refreshInterval = 500 * time.Millisecond

type App struct {
	app     *tview.Application
	tracker *tracker.Tracker

	proxyAddr  string
	targetAddr string
	startTime  time.Time
	statusMsg  string
	statusAt   time.Time

	header       *tview.TextView
	statsBar     *tview.TextView
	modelTable   *tview.Table
	requestTable *tview.Table
	footer       *tview.TextView
	layout       *tview.Flex
}

func New(t *tracker.Tracker, proxyAddr, targetAddr string) *App {
	a := &App{
		app:        tview.NewApplication(),
		tracker:    t,
		proxyAddr:  proxyAddr,
		targetAddr: targetAddr,
		startTime:  time.Now(),
	}
	a.buildUI()
	return a
}

func (a *App) Run() error {
	go a.refreshLoop()
	return a.app.Run()
}

func (a *App) buildUI() {
	a.header = tview.NewTextView().
		SetDynamicColors(true).
		SetTextAlign(tview.AlignLeft)
	a.header.
		SetBorder(true).
		SetTitle(" MISER ").
		SetTitleAlign(tview.AlignLeft).
		SetBorderColor(tcell.ColorDarkCyan).
		SetTitleColor(tcell.ColorAqua)

	a.statsBar = tview.NewTextView().
		SetDynamicColors(true).
		SetTextAlign(tview.AlignCenter)
	a.statsBar.SetBackgroundColor(tcell.ColorDarkSlateGray)

	a.modelTable = tview.NewTable().
		SetBorders(false).
		SetSelectable(true, false).
		SetFixed(1, 0)
	a.modelTable.
		SetBorder(true).
		SetTitle(" Models ").
		SetTitleAlign(tview.AlignLeft).
		SetBorderColor(tcell.ColorDarkCyan).
		SetTitleColor(tcell.ColorYellow)

	a.requestTable = tview.NewTable().
		SetBorders(false).
		SetSelectable(true, false).
		SetFixed(1, 0)
	a.requestTable.
		SetBorder(true).
		SetTitle(" Request Log ").
		SetTitleAlign(tview.AlignLeft).
		SetBorderColor(tcell.ColorDarkCyan).
		SetTitleColor(tcell.ColorYellow)

	a.footer = tview.NewTextView().
		SetDynamicColors(true).
		SetTextAlign(tview.AlignCenter)
	a.footer.SetBackgroundColor(tcell.ColorDarkSlateGray)

	a.layout = tview.NewFlex().SetDirection(tview.FlexRow).
		AddItem(a.header, 4, 0, false).
		AddItem(a.statsBar, 1, 0, false).
		AddItem(a.modelTable, 0, 1, false).
		AddItem(a.requestTable, 0, 3, true).
		AddItem(a.footer, 1, 0, false)

	a.app.SetInputCapture(func(event *tcell.EventKey) *tcell.EventKey {
		switch event.Key() {
		case tcell.KeyTab:
			a.toggleFocus()
			return nil
		case tcell.KeyRune:
			switch event.Rune() {
			case 'q':
				a.app.Stop()
				return nil
			case 'c':
				a.tracker.Clear()
				a.setStatus("Session cleared")
				return nil
			case 'e':
				a.export()
				return nil
			}
		}
		return event
	})

	a.app.SetRoot(a.layout, true).EnableMouse(true)
}

func (a *App) toggleFocus() {
	if a.app.GetFocus() == a.requestTable {
		a.app.SetFocus(a.modelTable)
	} else {
		a.app.SetFocus(a.requestTable)
	}
}

func (a *App) setStatus(msg string) {
	a.statusMsg = msg
	a.statusAt = time.Now()
}

func (a *App) refreshLoop() {
	tick := time.NewTicker(refreshInterval)
	defer tick.Stop()

	for range tick.C {
		a.app.QueueUpdateDraw(func() {
			a.renderHeader()
			a.renderStats()
			a.renderModels()
			a.renderRequests()
			a.renderFooter()
		})
	}
}

func (a *App) renderHeader() {
	uptime := time.Since(a.startTime).Truncate(time.Second)
	text := fmt.Sprintf(
		" [green]●[white] Proxy: [::b]%s[-::-]    [blue]↗[white] Target: [::b]%s[-::-]    [yellow]⏱[white] Uptime: [::b]%s[-::-]",
		a.proxyAddr, a.targetAddr, formatDuration(uptime),
	)
	a.header.SetText(text)
}

func (a *App) renderStats() {
	s := a.tracker.GetSummary()
	text := fmt.Sprintf(
		" [green::b]%s[-::-] cost    [white::b]%d[-::-] requests    [cyan::b]%s[-::-] input    [cyan::b]%s[-::-] output    [blue::b]%s[-::-] cache read    [blue::b]%s[-::-] cache write",
		formatCost(s.TotalCost), s.TotalRequests,
		formatTokens(s.TotalInput), formatTokens(s.TotalOutput),
		formatTokens(s.TotalCacheR), formatTokens(s.TotalCacheW),
	)
	a.statsBar.SetText(text)
}

func (a *App) renderModels() {
	a.modelTable.Clear()

	headers := []string{"MODEL", "REQS", "INPUT", "OUTPUT", "CACHE R", "CACHE W", "COST", "%"}
	for i, h := range headers {
		align := tview.AlignRight
		if i == 0 {
			align = tview.AlignLeft
		}
		a.modelTable.SetCell(0, i,
			tview.NewTableCell(" "+h+" ").
				SetTextColor(tcell.ColorYellow).
				SetAttributes(tcell.AttrBold).
				SetSelectable(false).
				SetAlign(align),
		)
	}

	stats := a.tracker.GetModelStats()
	summary := a.tracker.GetSummary()

	for i, ms := range stats {
		row := i + 1
		pct := 0.0
		if summary.TotalCost > 0 {
			pct = ms.TotalCost / summary.TotalCost * 100
		}
		a.setModelRow(row, ms, pct)
	}
}

func (a *App) setModelRow(row int, ms tracker.ModelStats, pct float64) {
	cells := []struct {
		text  string
		color tcell.Color
		align int
	}{
		{" " + shortModel(ms.Model) + " ", tcell.ColorWhite, tview.AlignLeft},
		{fmt.Sprintf(" %d ", ms.Requests), tcell.ColorWhite, tview.AlignRight},
		{" " + formatTokens(ms.InputTokens) + " ", tcell.ColorWhite, tview.AlignRight},
		{" " + formatTokens(ms.OutputTokens) + " ", tcell.ColorWhite, tview.AlignRight},
		{" " + formatTokens(ms.CacheRead) + " ", tcell.ColorSteelBlue, tview.AlignRight},
		{" " + formatTokens(ms.CacheWrite) + " ", tcell.ColorSteelBlue, tview.AlignRight},
		{" " + formatCost(ms.TotalCost) + " ", costColor(ms.TotalCost), tview.AlignRight},
		{fmt.Sprintf(" %.1f%% ", pct), tcell.ColorWhite, tview.AlignRight},
	}
	for i, c := range cells {
		a.modelTable.SetCell(row, i,
			tview.NewTableCell(c.text).
				SetTextColor(c.color).
				SetAlign(c.align),
		)
	}
}

func (a *App) renderRequests() {
	a.requestTable.Clear()

	headers := []string{"TIME", "MODEL", "INPUT", "OUTPUT", "COST", "LATENCY", "STATUS"}
	for i, h := range headers {
		align := tview.AlignRight
		if i <= 1 {
			align = tview.AlignLeft
		}
		a.requestTable.SetCell(0, i,
			tview.NewTableCell(" "+h+" ").
				SetTextColor(tcell.ColorYellow).
				SetAttributes(tcell.AttrBold).
				SetSelectable(false).
				SetAlign(align),
		)
	}

	recent := a.tracker.GetRecentRequests(500)
	for i, req := range recent {
		row := i + 1
		statusText := fmt.Sprintf("%d", req.StatusCode)
		statusColor := tcell.ColorGreen
		if req.Error != "" {
			statusText = "ERR"
			statusColor = tcell.ColorRed
		} else if req.StatusCode >= 400 {
			statusColor = tcell.ColorRed
		}

		cells := []struct {
			text  string
			color tcell.Color
			align int
		}{
			{" " + req.Timestamp.Format("15:04:05") + " ", tcell.ColorGray, tview.AlignLeft},
			{" " + shortModel(req.Model) + " ", tcell.ColorWhite, tview.AlignLeft},
			{" " + formatTokens(req.InputTokens) + " ", tcell.ColorWhite, tview.AlignRight},
			{" " + formatTokens(req.OutputTokens) + " ", tcell.ColorWhite, tview.AlignRight},
			{" " + formatCost(req.Cost) + " ", costColor(req.Cost), tview.AlignRight},
			{" " + formatLatency(req.Latency) + " ", tcell.ColorWhite, tview.AlignRight},
			{" " + statusText + " ", statusColor, tview.AlignRight},
		}
		for j, c := range cells {
			a.requestTable.SetCell(row, j,
				tview.NewTableCell(c.text).
					SetTextColor(c.color).
					SetAlign(c.align),
			)
		}
	}
}

func (a *App) renderFooter() {
	base := " [yellow]<q>[white] Quit  [yellow]<c>[white] Clear  [yellow]<e>[white] Export  [yellow]<Tab>[white] Switch Focus"
	if a.statusMsg != "" && time.Since(a.statusAt) < 3*time.Second {
		base += fmt.Sprintf("  [green]│ %s[-]", a.statusMsg)
	} else {
		a.statusMsg = ""
	}
	a.footer.SetText(base)
}

func (a *App) export() {
	requests := a.tracker.GetRequests()
	if len(requests) == 0 {
		a.setStatus("Nothing to export")
		return
	}

	filename := fmt.Sprintf("miser-export-%s.csv", time.Now().Format("2006-01-02-150405"))
	f, err := os.Create(filename)
	if err != nil {
		a.setStatus(fmt.Sprintf("Export failed: %v", err))
		return
	}
	defer f.Close()

	w := csv.NewWriter(f)
	w.Write([]string{"Time", "Model", "Input Tokens", "Output Tokens", "Cache Read", "Cache Write", "Cost", "Latency (s)", "Status"})
	for _, r := range requests {
		w.Write([]string{
			r.Timestamp.Format(time.RFC3339),
			r.Model,
			strconv.Itoa(r.InputTokens),
			strconv.Itoa(r.OutputTokens),
			strconv.Itoa(r.CacheRead),
			strconv.Itoa(r.CacheWrite),
			fmt.Sprintf("%.6f", r.Cost),
			fmt.Sprintf("%.3f", r.Latency.Seconds()),
			strconv.Itoa(r.StatusCode),
		})
	}
	w.Flush()
	a.setStatus(fmt.Sprintf("Exported %d rows → %s", len(requests), filename))
}

// --- formatting helpers ---

func formatTokens(n int) string {
	switch {
	case n >= 1_000_000:
		return fmt.Sprintf("%.1fM", float64(n)/1_000_000)
	case n >= 1_000:
		return fmt.Sprintf("%.1fK", float64(n)/1_000)
	default:
		return fmt.Sprintf("%d", n)
	}
}

func formatCost(c float64) string {
	switch {
	case c >= 10:
		return fmt.Sprintf("$%.2f", c)
	case c >= 1:
		return fmt.Sprintf("$%.3f", c)
	case c >= 0.01:
		return fmt.Sprintf("$%.4f", c)
	case c == 0:
		return "$0.00"
	default:
		return fmt.Sprintf("$%.5f", c)
	}
}

func formatLatency(d time.Duration) string {
	switch {
	case d >= time.Minute:
		return fmt.Sprintf("%.1fm", d.Minutes())
	case d >= time.Second:
		return fmt.Sprintf("%.1fs", d.Seconds())
	default:
		return fmt.Sprintf("%dms", d.Milliseconds())
	}
}

func formatDuration(d time.Duration) string {
	h := int(d.Hours())
	m := int(d.Minutes()) % 60
	s := int(d.Seconds()) % 60
	if h > 0 {
		return fmt.Sprintf("%dh %dm %ds", h, m, s)
	}
	if m > 0 {
		return fmt.Sprintf("%dm %ds", m, s)
	}
	return fmt.Sprintf("%ds", s)
}

func costColor(c float64) tcell.Color {
	switch {
	case c >= 1.0:
		return tcell.ColorRed
	case c >= 0.10:
		return tcell.ColorYellow
	default:
		return tcell.ColorGreen
	}
}

func shortModel(m string) string {
	parts := map[string]string{
		"claude-sonnet-4-20250514":    "claude-sonnet-4",
		"claude-opus-4-20250514":      "claude-opus-4",
		"claude-3-7-sonnet-20250219":  "claude-3.7-sonnet",
		"claude-3-5-sonnet-20241022":  "claude-3.5-sonnet",
		"claude-3-5-haiku-20241022":   "claude-3.5-haiku",
		"claude-3-opus-20240229":      "claude-3-opus",
	}
	if short, ok := parts[m]; ok {
		return short
	}
	if len(m) > 24 {
		return m[:24]
	}
	return m
}
