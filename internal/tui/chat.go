package tui

import (
	"bufio"
	"bytes"
	"crypto/rand"
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
	convID    string
	policy    string
}

func newUUID() string {
	var b [16]byte
	_, _ = rand.Read(b[:])
	b[6] = (b[6] & 0x0f) | 0x40 // version 4
	b[8] = (b[8] & 0x3f) | 0x80 // RFC 4122 variant
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:])
}

func Chat(addr string, maxTokens int) error {
	s := &session{
		addr:      strings.TrimRight(addr, "/"),
		client:    &http.Client{Timeout: 300 * time.Second},
		in:        bufio.NewScanner(os.Stdin),
		maxTokens: maxTokens,
		convID:    newUUID(),
	}
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
	fmt.Println(Cyan("│ ") + Dim(" LLM load balancer · v1.1") + Cyan("   │"))
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
func (s *session) fetchPolicies() (api.RoutingPolicyList, error) {
	var list api.RoutingPolicyList
	r, e := s.client.Get(s.addr + "/policies")
	if e != nil {
		return list, e
	}
	defer r.Body.Close()
	e = json.NewDecoder(r.Body).Decode(&list)
	return list, e
}
func (s *session) policyCommand(args []string) {
	list, e := s.fetchPolicies()
	if e != nil {
		fmt.Println(Red(e.Error()))
		return
	}
	current := s.policy
	if current == "" {
		current = list.Default
	}
	if len(args) == 0 {
		fmt.Println(Bold("Routing policies (current: " + current + "):"))
		for i, p := range list.Policies {
			mark := " "
			if p.Name == current {
				mark = "*"
			}
			fmt.Printf("  %s %s %s %s\n", mark, Cyan(strconv.Itoa(i+1)+"."), p.Name, Dim(p.Description))
		}
		fmt.Println(Dim("usage: /policy <name|number>"))
		return
	}
	for i, p := range list.Policies {
		if args[0] == p.Name || args[0] == strconv.Itoa(i+1) {
			s.policy = p.Name
			// Sessions pin a worker, so start fresh for the new policy to apply.
			s.convID = newUUID()
			fmt.Println(Dim("Routing policy set to " + p.Name + " (started a new conversation)"))
			return
		}
	}
	fmt.Println(Yellow("unknown routing policy"))
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
			s.convID = newUUID()
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
		fmt.Println("/models  /model <name|number>  /policy [name|number]  /new  /status  /quit")
	case "/new":
		s.convID = newUUID()
		fmt.Println(Dim("Started new conversation."))
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
				s.convID = newUUID()
			} else {
				fmt.Println(Yellow("unknown model"))
			}
		}
	case "/policy":
		s.policyCommand(f[1:])
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
	req, e := http.NewRequest("POST", s.addr+"/v1/chat/completions", bytes.NewReader(body))
	if e != nil {
		fmt.Println(Red("  ✗ " + e.Error()))
		return
	}
	req.Header.Set("Content-Type", "application/json")
	if s.convID != "" {
		req.Header.Set("X-Conversation-ID", s.convID)
	}
	if s.policy != "" {
		req.Header.Set("X-Meridian-Routing-Policy", s.policy)
	}
	r, e := s.client.Do(req)
	if e != nil {
		fmt.Println(Red("  ✗ " + e.Error()))
		return
	}
	defer r.Body.Close()
	if r.StatusCode != 200 {
		var x api.ErrorBody
		_ = json.NewDecoder(r.Body).Decode(&x)
		if x.Error.Type == "model_mismatch" {
			// A session is bound to one model; start a fresh conversation.
			s.convID = newUUID()
			fmt.Println(Red("  ✗ "+x.Error.Message) + Dim(" (started a new conversation)"))
			return
		}
		if x.Error.Type == "context_too_long" {
			fmt.Println(Red("  ✗ "+x.Error.Message) + Dim(" (use /new to start a fresh conversation)"))
			return
		}
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
	fmt.Println(Bold(fmt.Sprintf("%-4s%-16s%-6s%-8s%-12s%-10s%-8s%-8s%-10s", "ID", "MODEL", "PORT", "PID", "STATE", "IN-FLIGHT", "EST-TOK", "SERVED", "SESSIONS")))
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
		fmt.Printf("%-4s%-16s%-6d%-8d%s%-10d%-8d%-8d%-10d\n", w.ID, w.Model, w.Port, w.PID, padColor(state, plainState, 12), w.InFlight, w.EstTokensInFlight, w.Served, w.Sessions)
	}
}

func padColor(colored, plain string, width int) string {
	return colored + strings.Repeat(" ", max(0, width-len([]rune(plain))))
}
