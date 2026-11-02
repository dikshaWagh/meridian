package cli

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/dikshaWagh/meridian/internal/api"
)

type session struct {
	addr    string
	client  *http.Client
	in      *bufio.Scanner
	current string
}

// Chat runs the interactive REPL.
func Chat(addr string) error {
	s := &session{
		addr:   strings.TrimRight(addr, "/"),
		client: &http.Client{Timeout: 60 * time.Second},
		in:     bufio.NewScanner(os.Stdin),
	}
	printBanner()

	models, err := s.fetchModels()
	if err != nil {
		fmt.Println(Red("✗ cannot reach the balancer at " + s.addr + ". Start it with `meridian serve`."))
		return err
	}
	if len(models) == 0 {
		fmt.Println(Yellow("workers are still starting, try /models in a moment"))
	} else if !s.pickModel(models) {
		return nil
	}

	for {
		fmt.Print(s.prompt())
		if !s.in.Scan() {
			fmt.Println()
			fmt.Println(Dim("bye"))
			return nil
		}
		text := strings.TrimSpace(s.in.Text())
		switch {
		case text == "":
		case strings.HasPrefix(text, "/"):
			if !s.handleCommand(text) {
				fmt.Println(Dim("bye"))
				return nil
			}
		default:
			s.sendPrompt(text)
		}
	}
}

func printBanner() {
	fmt.Println(Cyan("╭──────────────────────────────╮"))
	fmt.Println(Cyan("│ ") + Bold(Cyan("        MERIDIAN")) + Cyan("             │"))
	fmt.Println(Cyan("│ ") + Dim(" LLM load balancer · v0") + Cyan("      │"))
	fmt.Println(Cyan("╰──────────────────────────────╯"))
	fmt.Println(Dim("type /help for commands"))
	fmt.Println()
}

func (s *session) prompt() string {
	if s.current == "" {
		return "(no model) " + Bold("❯") + " "
	}
	return Cyan(s.current) + " " + Bold("❯") + " "
}

func (s *session) fetchModels() ([]string, error) {
	resp, err := s.client.Get(s.addr + "/v1/models")
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	var list api.ModelList
	if err := json.NewDecoder(resp.Body).Decode(&list); err != nil {
		return nil, err
	}
	out := []string{}
	for _, m := range list.Data {
		out = append(out, m.ID)
	}
	return out, nil
}

// resolveModel accepts a menu number or a model name.
func resolveModel(input string, models []string) (string, bool) {
	input = strings.TrimSpace(input)
	if n, err := strconv.Atoi(input); err == nil && n >= 1 && n <= len(models) {
		return models[n-1], true
	}
	for _, m := range models {
		if m == input {
			return m, true
		}
	}
	return "", false
}

// pickModel shows the menu and asks until valid. Returns false on EOF.
func (s *session) pickModel(models []string) bool {
	fmt.Println(Bold("Available models:"))
	for i, m := range models {
		fmt.Printf("  %s %s\n", Cyan(strconv.Itoa(i+1)+"."), m)
	}
	for {
		fmt.Print("Choose a model (number or name): ")
		if !s.in.Scan() {
			return false
		}
		if m, ok := resolveModel(s.in.Text(), models); ok {
			s.current = m
			fmt.Println(Dim("using " + m))
			fmt.Println()
			return true
		}
		fmt.Println(Yellow("invalid choice, try again"))
	}
}

// handleCommand returns false when the user wants to quit.
func (s *session) handleCommand(text string) bool {
	fields := strings.Fields(text)
	switch fields[0] {
	case "/quit", "/exit":
		return false
	case "/help":
		fmt.Println(Bold("commands:"))
		fmt.Println("  /models            list models and pick one")
		fmt.Println("  /model <name|num>  switch model")
		fmt.Println("  /status            show worker health and load")
		fmt.Println("  /quit              exit")
	case "/models":
		models, err := s.fetchModels()
		if err != nil {
			fmt.Println(Red("✗ " + err.Error()))
		} else if len(models) == 0 {
			fmt.Println(Yellow("no healthy models yet"))
		} else if !s.pickModel(models) {
			return false
		}
	case "/model":
		if len(fields) < 2 {
			fmt.Println(Yellow("usage: /model <name or number>"))
			break
		}
		models, err := s.fetchModels()
		if err != nil {
			fmt.Println(Red("✗ " + err.Error()))
			break
		}
		if m, ok := resolveModel(fields[1], models); ok {
			s.current = m
			fmt.Println(Dim("using " + m))
		} else {
			fmt.Println(Yellow("unknown model, try /models"))
		}
	case "/status":
		s.printStatus()
	default:
		fmt.Println(Yellow("unknown command, try /help"))
	}
	return true
}

func (s *session) sendPrompt(text string) {
	if s.current == "" {
		fmt.Println(Yellow("no model selected, use /models"))
		return
	}
	body, _ := json.Marshal(api.ChatRequest{
		Model:    s.current,
		Messages: []api.Message{{Role: "user", Content: text}},
	})
	if enabled {
		fmt.Print(Dim("…"))
	}
	start := time.Now()
	resp, err := s.client.Post(s.addr+"/v1/chat/completions", "application/json", bytes.NewReader(body))
	elapsed := time.Since(start)
	if enabled {
		fmt.Print("\r\033[2K")
	}
	if err != nil {
		fmt.Println(Red("  ✗ " + err.Error()))
		return
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		var eb api.ErrorBody
		_ = json.NewDecoder(resp.Body).Decode(&eb)
		msg := eb.Error.Message
		if msg == "" {
			msg = resp.Status
		}
		fmt.Println(Red("  ✗ "+msg) + " " + Dim("("+strconv.Itoa(resp.StatusCode)+")"))
		return
	}
	var cr api.ChatResponse
	if err := json.NewDecoder(resp.Body).Decode(&cr); err != nil || len(cr.Choices) == 0 {
		fmt.Println(Red("  ✗ bad response from balancer"))
		return
	}
	worker := resp.Header.Get("X-Meridian-Worker")
	fmt.Printf("  %s %s\n", WorkerColor(worker, worker),
		Dim("· "+s.current+" · "+strconv.FormatInt(elapsed.Milliseconds(), 10)+"ms"))
	fmt.Println(cr.Choices[0].Message.Content)
	fmt.Println()
}

func (s *session) printStatus() {
	resp, err := s.client.Get(s.addr + "/status")
	if err != nil {
		fmt.Println(Red("✗ " + err.Error()))
		return
	}
	defer resp.Body.Close()
	var st api.StatusResponse
	if err := json.NewDecoder(resp.Body).Decode(&st); err != nil {
		fmt.Println(Red("✗ " + err.Error()))
		return
	}
	// Pad plain text first, then color: ANSI codes would break alignment.
	row := func(cells ...string) string {
		widths := []int{4, 16, 6, 8, 8, 10, 8}
		var sb strings.Builder
		for i, c := range cells {
			sb.WriteString(fmt.Sprintf("%-*s", widths[i], c))
		}
		return sb.String()
	}
	fmt.Println(Bold(row("ID", "MODEL", "PORT", "PID", "HEALTH", "IN-FLIGHT", "SERVED")))
	for _, w := range st.Workers {
		health := Red(fmt.Sprintf("%-8s", "● down"))
		if w.Healthy {
			health = Green(fmt.Sprintf("%-8s", "● up"))
		}
		fmt.Println(
			WorkerColor(w.ID, fmt.Sprintf("%-4s", w.ID)) +
				row("", w.Model, strconv.Itoa(w.Port), strconv.Itoa(w.PID))[4:] +
				health +
				fmt.Sprintf("%-10d%-8d", w.InFlight, w.Served))
	}
	fmt.Println()
}
