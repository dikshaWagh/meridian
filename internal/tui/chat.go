package tui

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"github.com/dikshaWagh/meridian/internal/api"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"
)

type session struct {
	addr      string
	client    *http.Client
	in        *bufio.Scanner
	current   string
	maxTokens int
}

func Chat(addr string, maxTokens int) error {
	s := &session{addr: strings.TrimRight(addr, "/"), client: &http.Client{Timeout: 300 * time.Second}, in: bufio.NewScanner(os.Stdin), maxTokens: maxTokens}
	printBanner()
	if _, e := s.healthz(); e != nil {
		fmt.Println(Red("✗ cannot reach balancer at " + s.addr))
		return e
	}
	ms, e := s.fetchModels()
	if e != nil {
		return e
	}
	if len(ms) == 0 {
		fmt.Println(Yellow("workers are still starting, try /models in a moment"))
	} else if !s.pickModel(ms) {
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
		if text == "" {
			continue
		}
		if strings.HasPrefix(text, "/") {
			if !s.command(text) {
				fmt.Println(Dim("bye"))
				return nil
			}
		} else {
			s.send(text)
		}
	}
}
func printBanner() {
	fmt.Println(Cyan("╭──────────────────────────────╮"))
	fmt.Println(Cyan("│ ") + Bold(Cyan("        MERIDIAN")) + Cyan("             │"))
	fmt.Println(Cyan("│ ") + Dim(" LLM load balancer · v1") + Cyan("      │"))
	fmt.Println(Cyan("╰──────────────────────────────╯"))
	fmt.Println(Dim("type /help for commands"))
	fmt.Println()
}
func (s *session) prompt() string {
	if s.current == "" {
		return "(no model) " + Bold("❯ ")
	}
	return Cyan(s.current) + " " + Bold("❯ ")
}
func (s *session) healthz() ([]byte, error) {
	r, e := s.client.Get(s.addr + "/healthz")
	if e != nil {
		return nil, e
	}
	defer r.Body.Close()
	if r.StatusCode != 200 {
		return nil, fmt.Errorf("server returned %s", r.Status)
	}
	var b bytes.Buffer
	_, e = b.ReadFrom(r.Body)
	return b.Bytes(), e
}
func (s *session) fetchModels() ([]string, error) {
	r, e := s.client.Get(s.addr + "/v1/models")
	if e != nil {
		return nil, e
	}
	defer r.Body.Close()
	var l api.ModelList
	e = json.NewDecoder(r.Body).Decode(&l)
	if e != nil {
		return nil, e
	}
	o := []string{}
	for _, m := range l.Data {
		o = append(o, m.ID)
	}
	return o, nil
}
func resolve(in string, ms []string) (string, bool) {
	if n, e := strconv.Atoi(strings.TrimSpace(in)); e == nil && n > 0 && n <= len(ms) {
		return ms[n-1], true
	}
	for _, m := range ms {
		if m == strings.TrimSpace(in) {
			return m, true
		}
	}
	return "", false
}
func (s *session) pickModel(ms []string) bool {
	fmt.Println(Bold("Available models:"))
	for i, m := range ms {
		fmt.Printf("  %s %s\n", Cyan(strconv.Itoa(i+1)+"."), m)
	}
	for {
		fmt.Print("Choose a model (number or name): ")
		if !s.in.Scan() {
			return false
		}
		if m, ok := resolve(s.in.Text(), ms); ok {
			s.current = m
			return true
		}
		fmt.Println(Yellow("invalid choice, try again"))
	}
}
func (s *session) command(text string) bool {
	f := strings.Fields(text)
	switch f[0] {
	case "/quit", "/exit":
		return false
	case "/help":
		fmt.Println("/models  /model <name|number>  /status  /quit")
	case "/models":
		ms, e := s.fetchModels()
		if e != nil {
			fmt.Println(Red(e.Error()))
		} else if len(ms) == 0 {
			fmt.Println(Yellow("no healthy models yet"))
		} else {
			s.pickModel(ms)
		}
	case "/model":
		if len(f) < 2 {
			fmt.Println(Yellow("usage: /model <name or number>"))
			break
		}
		ms, e := s.fetchModels()
		if e == nil {
			if m, ok := resolve(f[1], ms); ok {
				s.current = m
			} else {
				fmt.Println(Yellow("unknown model"))
			}
		}
	case "/status":
		s.status()
	default:
		fmt.Println(Yellow("unknown command, try /help"))
	}
	return true
}
func (s *session) send(text string) {
	if s.current == "" {
		fmt.Println(Yellow("no model selected, use /models"))
		return
	}
	body, _ := json.Marshal(api.ChatRequest{Model: s.current, Messages: []api.Message{{Role: "user", Content: text}}, MaxTokens: s.maxTokens})
	start := time.Now()
	r, e := s.client.Post(s.addr+"/v1/chat/completions", "application/json", bytes.NewReader(body))
	if e != nil {
		fmt.Println(Red("  ✗ " + e.Error()))
		return
	}
	defer r.Body.Close()
	if r.StatusCode != 200 {
		var x api.ErrorBody
		_ = json.NewDecoder(r.Body).Decode(&x)
		fmt.Println(Red("  ✗ " + x.Error.Message))
		return
	}
	var c api.ChatResponse
	if e = json.NewDecoder(r.Body).Decode(&c); e != nil || len(c.Choices) == 0 {
		fmt.Println(Red("  ✗ bad response"))
		return
	}
	ei := r.Header.Get("X-Meridian-Est-Input-Tokens")
	eo := r.Header.Get("X-Meridian-Est-Output-Tokens")
	fmt.Printf("  %s %s\n", WorkerColor(r.Header.Get("X-Meridian-Worker"), r.Header.Get("X-Meridian-Worker")), Dim("· "+s.current+" · "+time.Since(start).Round(time.Millisecond).String()+" · est ~"+ei+" in / ≤"+eo+" out · actual "+strconv.Itoa(c.Usage.PromptTokens)+" in / "+strconv.Itoa(c.Usage.CompletionTokens)+" out"))
	fmt.Println(c.Choices[0].Message.Content)
	fmt.Println()
}
func (s *session) status() {
	r, e := s.client.Get(s.addr + "/status")
	if e != nil {
		fmt.Println(Red(e.Error()))
		return
	}
	defer r.Body.Close()
	var st api.StatusResponse
	if json.NewDecoder(r.Body).Decode(&st) != nil {
		return
	}
	fmt.Println(Bold(fmt.Sprintf("%-4s%-16s%-6s%-8s%-12s%-10s%-8s%-8s", "ID", "MODEL", "PORT", "PID", "STATE", "IN-FLIGHT", "EST-TOK", "SERVED")))
	for _, w := range st.Workers {
		plainState := "● unhealthy"
		if w.State == "healthy" {
			plainState = "● healthy"
		} else if w.State == "starting" {
			plainState = "● starting"
		}
		state := plainState
		if plainState == "● healthy" {
			state = Green(plainState)
		} else if plainState == "● starting" {
			state = Yellow(plainState)
		} else {
			state = Red(plainState)
		}
		fmt.Printf("%-4s%-16s%-6d%-8d%s%-10d%-8d%-8d\n", w.ID, w.Model, w.Port, w.PID, padColor(state, plainState, 12), w.InFlight, w.EstTokensInFlight, w.Served)
	}
}

func padColor(colored, plain string, width int) string {
	return colored + strings.Repeat(" ", max(0, width-len([]rune(plain))))
}
