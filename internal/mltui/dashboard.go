// Package mltui renders the ML inference balancer view. It is a separate view
// from the LLM chat TUI in internal/tui, which is left untouched.
package mltui

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"sort"
	"strings"
	"syscall"
	"time"

	"github.com/dikshaWagh/meridian/internal/ml"
	"github.com/dikshaWagh/meridian/internal/tui"
)

// Dashboard polls the ML status endpoint and redraws periodically.
func Dashboard(addr string, interval time.Duration, events int) error {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if interval <= 0 {
		interval = time.Second
	}
	base := strings.TrimRight(addr, "/")
	client := &http.Client{Timeout: 5 * time.Second}

	draw := func() {
		st, err := fetch(ctx, client, base)
		clearScreen()
		if err != nil {
			fmt.Println(tui.Red("✗ cannot reach ML balancer at " + base + ": " + err.Error()))
			fmt.Println(tui.Dim("is meridian-server running with --ml ?"))
			return
		}
		render(st, base, events)
	}

	draw()
	t := time.NewTicker(interval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			fmt.Println(tui.Dim("bye"))
			return nil
		case <-t.C:
			draw()
		}
	}
}

func fetch(ctx context.Context, client *http.Client, base string) (ml.StatusResponse, error) {
	var st ml.StatusResponse
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, base+"/ml/status", nil)
	if err != nil {
		return st, err
	}
	resp, err := client.Do(req)
	if err != nil {
		return st, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return st, fmt.Errorf("status %s", resp.Status)
	}
	err = json.NewDecoder(resp.Body).Decode(&st)
	return st, err
}

func clearScreen() { fmt.Print("\033[H\033[2J") }

func render(st ml.StatusResponse, base string, events int) {
	fmt.Println(tui.Cyan("╭────────────────────────────────────────────╮"))
	fmt.Println(tui.Cyan("│ ") + tui.Bold(tui.Cyan("   MERIDIAN · ML INFERENCE BALANCER")) + tui.Cyan("       │"))
	fmt.Println(tui.Cyan("╰────────────────────────────────────────────╯"))
	fmt.Println(tui.Dim(fmt.Sprintf("%s · score = %.2f·latency + %.2f·queue + %.2f·gpu + %.2f·cpu · lowest wins",
		base, st.Weights.Latency, st.Weights.Queue, st.Weights.GPU, st.Weights.CPU)))
	fmt.Println()

	renderModels(st)
	renderWorkers(st)
	renderTotals(st)
	renderHost(st)
	renderLog(st, events)
}

func renderModels(st ml.StatusResponse) {
	if len(st.Models) == 0 {
		fmt.Println(tui.Yellow("no ML models configured"))
		fmt.Println()
		return
	}
	var parts []string
	for _, m := range st.Models {
		label := fmt.Sprintf("%s (%d/%d healthy)", m.Model, m.Healthy, m.Workers)
		if m.Healthy == 0 {
			parts = append(parts, tui.Red(label))
		} else if m.Healthy < m.Workers {
			parts = append(parts, tui.Yellow(label))
		} else {
			parts = append(parts, tui.Green(label))
		}
	}
	fmt.Println(tui.Bold("MODELS  ") + strings.Join(parts, tui.Dim(" · ")))
	fmt.Println()
}

func renderWorkers(st ml.StatusResponse) {
	// Latest decision per model gives the live score of every candidate.
	scores := map[string]ml.CandidateScore{}
	for _, ev := range st.Recent {
		for _, c := range ev.Candidates {
			if c.Scored {
				scores[c.WorkerID] = c
			}
		}
	}
	selected := map[string]bool{}
	if len(st.Recent) > 0 {
		selected[st.Recent[len(st.Recent)-1].Selected] = true
	}

	workers := append([]ml.WorkerStatus(nil), st.Workers...)
	sort.SliceStable(workers, func(i, j int) bool {
		if workers[i].Model != workers[j].Model {
			return workers[i].Model < workers[j].Model
		}
		return workers[i].ID < workers[j].ID
	})

	fmt.Println(tui.Bold(fmt.Sprintf("%-12s%-12s%-13s%-10s%-11s%-9s%-9s%-9s%s",
		"WORKER", "MODEL", "STATE", "IN-FLIGHT", "LATENCY", "CPU", "GPU", "SCORE", "SERVED")))
	for _, w := range workers {
		scorePlain := "–"
		if c, ok := scores[w.ID]; ok {
			scorePlain = fmt.Sprintf("%.3f", c.FinalScore)
		}
		score := scorePlain
		if selected[w.ID] {
			score = tui.Green(scorePlain)
		}
		mark := " "
		if selected[w.ID] {
			mark = tui.Green("▸")
		}
		fmt.Printf("%s%s%-12s%s%-10d%-11s%-9s%-9s%s%d\n",
			mark,
			padPlain(tui.WorkerColor(w.ID, w.ID), w.ID, 11),
			trunc(w.Model, 11),
			padState(stateCell(w.State), w.State, 13),
			w.InFlight,
			latencyCell(w.Latency),
			pctCell(w.Telemetry.CPUPercent, w.Telemetry.CPUAvailable),
			pctCell(w.Telemetry.GPUPercent, w.Telemetry.GPUAvailable),
			padPlain(score, scorePlain, 9),
			w.Served,
		)
	}
	fmt.Println()

	if len(st.Recent) > 0 {
		last := st.Recent[len(st.Recent)-1]
		if last.Selected != "" {
			fmt.Println(tui.Bold("SELECTED  ") + tui.WorkerColor(last.Selected, last.Selected) + tui.Dim("  "+last.Reason))
		} else {
			fmt.Println(tui.Bold("SELECTED  ") + tui.Red("none") + tui.Dim("  "+last.Reason))
		}
		fmt.Println()
	}
}

func renderTotals(st ml.StatusResponse) {
	t := st.Totals
	rate := tui.Green(fmt.Sprintf("%.1f%%", t.SuccessRate*100))
	if t.Served > 0 && t.SuccessRate < 0.95 {
		rate = tui.Yellow(fmt.Sprintf("%.1f%%", t.SuccessRate*100))
	}
	if t.Served > 0 && t.SuccessRate < 0.5 {
		rate = tui.Red(fmt.Sprintf("%.1f%%", t.SuccessRate*100))
	}
	fmt.Println(tui.Bold("REQUESTS") + fmt.Sprintf("  total=%d  success=%d  failures=%d  rate=%s  avg=%.0fms  in-flight=%d",
		t.Served, t.Success, t.Failures, rate, t.AvgLatencyMS, t.InFlight))
}

func renderHost(st ml.StatusResponse) {
	cpu := tui.Dim("cpu n/a")
	if st.Host.CPUAvailable {
		cpu = fmt.Sprintf("cpu %.0f%%", st.Host.CPUPercent)
	}
	gpu := tui.Dim("gpu n/a")
	if st.Host.GPUAvailable && len(st.Host.GPUs) > 0 {
		var gs []string
		for _, g := range st.Host.GPUs {
			gs = append(gs, fmt.Sprintf("gpu%d %.0f%% (mem %.0f%%)", g.Index, g.UtilPercent, g.MemPercent))
		}
		gpu = strings.Join(gs, " ")
	}
	fmt.Println(tui.Bold("HOST    ") + "  " + cpu + "  " + gpu)
	fmt.Println()
}

func renderLog(st ml.StatusResponse, events int) {
	fmt.Println(tui.Bold("ROUTING LOG"))
	if len(st.Recent) == 0 {
		fmt.Println(tui.Dim("  no requests yet · POST /v1/predict to see routing decisions"))
		return
	}
	if events <= 0 {
		events = 8
	}
	start := len(st.Recent) - events
	if start < 0 {
		start = 0
	}
	for _, ev := range st.Recent[start:] {
		var parts []string
		for _, c := range ev.Candidates {
			if c.Scored {
				parts = append(parts, fmt.Sprintf("%s healthy score=%.2f", c.WorkerID, c.FinalScore))
			} else {
				parts = append(parts, tui.Red(fmt.Sprintf("%s %s", c.WorkerID, strings.ToLower(c.State))))
			}
		}
		fmt.Printf("%s model=%s | %s\n", tui.Dim("["+ev.Time+"]"), ev.Model, strings.Join(parts, tui.Dim(" | ")))
		if ev.Selected != "" {
			fmt.Printf("%s selected %s → %s latency=%.0fms\n",
				tui.Dim("["+ev.Time+"]"), tui.WorkerColor(ev.Selected, ev.Selected), statusCell(ev.Status), ev.LatencyMS)
		} else {
			fmt.Printf("%s %s %s\n", tui.Dim("["+ev.Time+"]"), statusCell(ev.Status), tui.Dim(ev.Error))
		}
	}
}

func stateCell(state string) string {
	switch state {
	case ml.StateHealthy.String():
		return tui.Green("● HEALTHY")
	case ml.StateStarting.String():
		return tui.Yellow("● STARTING")
	case ml.StateStopped.String():
		return tui.Dim("● STOPPED")
	default:
		return tui.Red("● UNHEALTHY")
	}
}

func statusCell(status int) string {
	switch {
	case status >= 200 && status < 300:
		return tui.Green(fmt.Sprint(status))
	case status >= 500 || status == 0:
		return tui.Red(fmt.Sprint(status))
	default:
		return tui.Yellow(fmt.Sprint(status))
	}
}

func latencyCell(l ml.LatencyStats) string {
	if l.Samples == 0 {
		return "–"
	}
	return fmt.Sprintf("%.0fms", l.AvgMS)
}

func pctCell(v float64, ok bool) string {
	if !ok {
		return "n/a"
	}
	return fmt.Sprintf("%.0f%%", v)
}

func trunc(s string, n int) string {
	if r := []rune(s); len(r) > n {
		return string(r[:n])
	}
	return s
}

// padPlain aligns a coloured cell using the width of its plain text.
func padPlain(colored, plain string, width int) string {
	w := width - len([]rune(plain))
	if w < 1 {
		w = 1
	}
	return colored + strings.Repeat(" ", w)
}

// padState aligns a state cell, accounting for its "● " bullet prefix.
func padState(colored, plain string, width int) string {
	return padPlain(colored, plain+"● ", width)
}
