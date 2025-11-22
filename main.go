package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"time"
)

type TargetConfig struct {
	Name  string `json:"name,omitempty"`
	IPv4  string `json:"ipv4,omitempty"`
	IPv6  string `json:"ipv6,omitempty"`
	DPort int    `json:"dport,omitempty"`
}

type Config struct {
	Targets  []TargetConfig `json:"targets,omitempty"`
	IPv4     string         `json:"ipv4,omitempty"` // legacy fallback
	IPv6     string         `json:"ipv6,omitempty"`
	DPort    int            `json:"dport,omitempty"`
	Webhook  string         `json:"webhook"`
	Host     string         `json:"hostname"`
	Delay    int            `json:"delay"`    // seconds
	Timeout  int            `json:"timeout"`  // seconds
	Timezone string         `json:"timezone"` // e.g. "Europe/Berlin"
}

// Discord structures

type discordEmbedField struct {
	Name   string `json:"name"`
	Value  string `json:"value"`
	Inline bool   `json:"inline"`
}

type discordEmbed struct {
	Title       string              `json:"title"`
	Description string              `json:"description,omitempty"`
	Color       int                 `json:"color,omitempty"`
	Fields      []discordEmbedField `json:"fields,omitempty"`
	Timestamp   string              `json:"timestamp,omitempty"`
}

type discordWebhookPayload struct {
	Content string         `json:"content,omitempty"`
	Embeds  []discordEmbed `json:"embeds,omitempty"`
}

// Target state (per IP version)

type TargetState struct {
	Name      string
	IP        string
	IPVersion string
	Network   string
	Port      int
	Down      bool
	DownSince time.Time
}

// Rotating log writer

type rotatingWriter struct {
	path    string
	maxSize int64
	file    *os.File
}

func newRotatingWriter(path string, maxSize int64) (*rotatingWriter, error) {
	w := &rotatingWriter{path: path, maxSize: maxSize}
	if err := w.open(); err != nil {
		return nil, err
	}
	return w, nil
}

func (w *rotatingWriter) open() error {
	if w.file != nil {
		w.file.Close()
	}
	f, err := os.OpenFile(w.path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0644)
	if err != nil {
		return err
	}
	w.file = f
	return nil
}

func (w *rotatingWriter) rotate() error {
	if w.file != nil {
		w.file.Close()
	}
	_ = os.Rename(w.path, w.path+".1")
	f, err := os.OpenFile(w.path, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0644)
	if err != nil {
		return err
	}
	w.file = f
	return nil
}

func (w *rotatingWriter) Write(p []byte) (int, error) {
	if w.file == nil {
		if err := w.open(); err != nil {
			return 0, err
		}
	}
	fi, err := w.file.Stat()
	if err == nil && fi.Size()+int64(len(p)) > w.maxSize {
		if err := w.rotate(); err != nil {
			return 0, err
		}
	}
	return w.file.Write(p)
}

func (w *rotatingWriter) Close() error {
	if w.file != nil {
		return w.file.Close()
	}
	return nil
}

// Config loading

func loadConfig(path string) (Config, error) {
	f, err := os.Open(path)
	if err != nil {
		return Config{}, err
	}
	defer f.Close()

	var cfg Config
	if err := json.NewDecoder(f).Decode(&cfg); err != nil {
		return Config{}, err
	}
	return cfg, nil
}

// TCP ping

func tcpPing(ip string, port int, timeoutSec int, network string) error {
	addr := net.JoinHostPort(ip, strconv.Itoa(port))
	timeout := time.Duration(timeoutSec) * time.Second
	conn, err := net.DialTimeout(network, addr, timeout)
	if err != nil {
		return err
	}
	conn.Close()
	return nil
}

// MTR

func runMTR(ip string) (string, error) {
	cmd := exec.Command("mtr", "-rwbzc", "10", ip)
	out, err := cmd.CombinedOutput()
	return string(out), err
}

func saveMTRToFile(mtrOut string, ts time.Time, t *TargetState) {
	exePath, err := os.Executable()
	if err != nil {
		log.Printf("cannot determine executable path for mtr dir: %v", err)
		return
	}
	baseDir := filepath.Dir(exePath)
	dir := filepath.Join(baseDir, "mtr")

	if err := os.MkdirAll(dir, 0755); err != nil {
		log.Printf("cannot create mtr dir %s: %v", dir, err)
		return
	}

	name := t.Name
	if name == "" {
		name = "target"
	}
	filename := fmt.Sprintf("%s_%s_%s_%d.txt",
		ts.Format("2006-01-02_15-04-05"),
		name,
		t.IPVersion,
		t.Port,
	)
	path := filepath.Join(dir, filename)
	if err := os.WriteFile(path, []byte(mtrOut), 0644); err != nil {
		log.Printf("cannot write mtr file %s: %v", path, err)
	}
}

// Discord helpers

func sendToDiscord(webhook string, payload discordWebhookPayload) error {
	body, err := json.Marshal(payload)
	if err != nil {
		return err
	}

	req, err := http.NewRequest("POST", webhook, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")

	client := &http.Client{
		Timeout: 10 * time.Second,
	}
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("discord webhook status %d", resp.StatusCode)
	}
	return nil
}

func getHostname(cfg Config) string {
	if cfg.Host != "" {
		return cfg.Host
	}
	hn, err := os.Hostname()
	if err != nil {
		return "unknown"
	}
	return hn
}

// Alerts

func sendDownAlert(cfg Config, t *TargetState, err error, mtrOut string, ts time.Time) {
	host := getHostname(cfg)

	targetName := t.Name
	if targetName == "" {
		targetName = "-"
	}

	fields := []discordEmbedField{
		{Name: "Target", Value: targetName, Inline: true},
		{Name: "Hostname", Value: host, Inline: true},
		{Name: "IP", Value: t.IP, Inline: true},
		{Name: "IP Version", Value: t.IPVersion, Inline: true},
		{Name: "Port", Value: strconv.Itoa(t.Port), Inline: true},
		{Name: "Delay (s)", Value: strconv.Itoa(cfg.Delay), Inline: true},
		{Name: "Timeout (s)", Value: strconv.Itoa(cfg.Timeout), Inline: true},
		{Name: "Status", Value: "DOWN", Inline: true},
		{Name: "Error", Value: err.Error(), Inline: false},
	}

	desc := ""
	if mtrOut != "" {
		desc = "```text\n" + mtrOut + "\n```"
	}

	embed := discordEmbed{
		Title:       "Port monitor alert (DOWN)",
		Color:       0xFF0000,
		Fields:      fields,
		Timestamp:   ts.Format(time.RFC3339),
		Description: desc,
	}

	payload := discordWebhookPayload{
		Embeds: []discordEmbed{embed},
	}

	if derr := sendToDiscord(cfg.Webhook, payload); derr != nil {
		log.Printf("error sending DOWN alert to discord: %v", derr)
	}
}

func sendUpAlert(cfg Config, t *TargetState, downSince time.Time, ts time.Time) {
	host := getHostname(cfg)
	dur := ts.Sub(downSince).Round(time.Second)

	targetName := t.Name
	if targetName == "" {
		targetName = "-"
	}

	fields := []discordEmbedField{
		{Name: "Target", Value: targetName, Inline: true},
		{Name: "Hostname", Value: host, Inline: true},
		{Name: "IP", Value: t.IP, Inline: true},
		{Name: "IP Version", Value: t.IPVersion, Inline: true},
		{Name: "Port", Value: strconv.Itoa(t.Port), Inline: true},
		{Name: "Delay (s)", Value: strconv.Itoa(cfg.Delay), Inline: true},
		{Name: "Timeout (s)", Value: strconv.Itoa(cfg.Timeout), Inline: true},
		{Name: "Status", Value: "UP", Inline: true},
		{Name: "Downtime", Value: dur.String(), Inline: false},
	}

	embed := discordEmbed{
		Title:     "Port monitor recovery (UP)",
		Color:     0x00FF00,
		Fields:    fields,
		Timestamp: ts.Format(time.RFC3339),
	}

	payload := discordWebhookPayload{
		Embeds: []discordEmbed{embed},
	}

	if derr := sendToDiscord(cfg.Webhook, payload); derr != nil {
		log.Printf("error sending UP alert to discord: %v", derr)
	}
}

// Per-target check with state

func checkTarget(cfg Config, t *TargetState, loc *time.Location) {
	if t == nil || t.IP == "" {
		return
	}

	now := time.Now().In(loc)
	err := tcpPing(t.IP, t.Port, cfg.Timeout, t.Network)

	if err == nil {
		if t.Down {
			log.Printf("UP %s %s %s:%d after %s",
				t.IPVersion, t.Name, t.IP, t.Port, now.Sub(t.DownSince).Round(time.Second))
			sendUpAlert(cfg, t, t.DownSince, now)
			t.Down = false
		}
		return
	}

	if !t.Down {
		t.Down = true
		t.DownSince = now

		log.Printf("DOWN %s %s %s:%d error=%v",
			t.IPVersion, t.Name, t.IP, t.Port, err)

		var mtrOut string
		if nerr, ok := err.(net.Error); ok && nerr.Timeout() {
			mtrText, mtrErr := runMTR(t.IP)
			if mtrErr != nil {
				log.Printf("mtr error for %s: %v", t.IP, mtrErr)
				mtrOut = "mtr error: " + mtrErr.Error() + "\n\n" + mtrText
			} else {
				mtrOut = mtrText
			}
			if mtrOut != "" {
				saveMTRToFile(mtrOut, now, t)
			}
		} else {
			mtrOut = "no mtr (non-timeout error)"
		}

		sendDownAlert(cfg, t, err, mtrOut, now)
	}
}

// Goroutine per target

func monitorTarget(cfg Config, t *TargetState, loc *time.Location) {
	for {
		checkTarget(cfg, t, loc)
		time.Sleep(time.Duration(cfg.Delay) * time.Second)
	}
}

// main

func main() {
	cfgPath := "config.json"
	if len(os.Args) > 1 {
		cfgPath = os.Args[1]
	}

	const maxLogSize = 50 * 1024 * 1024
	rw, err := newRotatingWriter("portwatch.log", maxLogSize)
	if err != nil {
		log.Fatalf("cannot set up log file: %v", err)
	}
	defer rw.Close()

	log.SetOutput(io.MultiWriter(os.Stdout, rw))
	log.SetFlags(log.LstdFlags)

	cfg, err := loadConfig(cfgPath)
	if err != nil {
		log.Fatalf("cannot load config: %v", err)
	}

	// Legacy fallback: single ipv4/ipv6/dport
	if len(cfg.Targets) == 0 && (cfg.IPv4 != "" || cfg.IPv6 != "" || cfg.DPort != 0) {
		cfg.Targets = []TargetConfig{
			{
				Name:  "default",
				IPv4:  cfg.IPv4,
				IPv6:  cfg.IPv6,
				DPort: cfg.DPort,
			},
		}
	}

	if cfg.Webhook == "" {
		log.Fatal("webhook missing in config")
	}
	if cfg.Delay <= 0 {
		cfg.Delay = 30
	}
	if cfg.Timeout <= 0 {
		cfg.Timeout = 5
	}
	if cfg.Timezone == "" {
		cfg.Timezone = "Europe/Berlin"
	}

	loc, err := time.LoadLocation(cfg.Timezone)
	if err != nil {
		log.Fatalf("cannot load timezone %q: %v", cfg.Timezone, err)
	}
	time.Local = loc

	if len(cfg.Targets) == 0 {
		log.Fatal("no targets configured")
	}

	var states []*TargetState
	for _, tc := range cfg.Targets {
		if tc.DPort <= 0 {
			continue
		}
		if tc.IPv4 != "" {
			st := &TargetState{
				Name:      tc.Name,
				IP:        tc.IPv4,
				IPVersion: "IPv4",
				Network:   "tcp4",
				Port:      tc.DPort,
			}
			states = append(states, st)
		}
		if tc.IPv6 != "" {
			st := &TargetState{
				Name:      tc.Name,
				IP:        tc.IPv6,
				IPVersion: "IPv6",
				Network:   "tcp6",
				Port:      tc.DPort,
			}
			states = append(states, st)
		}
	}

	if len(states) == 0 {
		log.Fatal("no valid targets (missing IPs or ports)")
	}

	log.Printf("starting port monitor: targets=%d delay=%ds timeout=%ds timezone=%s",
		len(states), cfg.Delay, cfg.Timeout, cfg.Timezone)

	for _, st := range states {
		go monitorTarget(cfg, st, loc)
	}

	select {}
}
