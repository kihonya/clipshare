package main

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"time"
)

type Config struct {
	Listen string `json:"listen"` // "auto" or host:port
	Port   int    `json:"port"`
	Token  string `json:"token"`
}

func exeDir() string {
	exe, err := os.Executable()
	if err != nil {
		wd, _ := os.Getwd()
		return wd
	}
	return filepath.Dir(exe)
}

func genToken() string {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		panic(err)
	}
	return hex.EncodeToString(b)
}

func loadOrCreateConfig(path string) (*Config, error) {
	cfg := &Config{Listen: "auto", Port: 8765}
	data, err := os.ReadFile(path)
	if err == nil {
		if err := json.Unmarshal(data, cfg); err != nil {
			return nil, fmt.Errorf("config parse: %w", err)
		}
		if cfg.Port == 0 {
			cfg.Port = 8765
		}
		if cfg.Listen == "" {
			cfg.Listen = "auto"
		}
		if cfg.Token != "" {
			return cfg, nil
		}
		// fall through: token missing, generate and save
	} else if !os.IsNotExist(err) {
		return nil, err
	}
	cfg.Token = genToken()
	out, _ := json.MarshalIndent(cfg, "", "  ")
	if err := os.WriteFile(path, out, 0o600); err != nil {
		return nil, fmt.Errorf("config write: %w", err)
	}
	return cfg, nil
}

// findTailscaleIP returns the first IPv4 address in 100.64.0.0/10.
func findTailscaleIP() (string, bool) {
	ifs, err := net.Interfaces()
	if err != nil {
		return "", false
	}
	for _, ifc := range ifs {
		if ifc.Flags&net.FlagUp == 0 {
			continue
		}
		addrs, err := ifc.Addrs()
		if err != nil {
			continue
		}
		for _, a := range addrs {
			var ip net.IP
			switch v := a.(type) {
			case *net.IPNet:
				ip = v.IP
			case *net.IPAddr:
				ip = v.IP
			}
			ip4 := ip.To4()
			if ip4 == nil {
				continue
			}
			if ip4[0] == 100 && ip4[1] >= 64 && ip4[1] <= 127 {
				return ip4.String(), true
			}
		}
	}
	return "", false
}

func listenAddrs(cfg *Config) []string {
	if cfg.Listen != "auto" && cfg.Listen != "" {
		return []string{cfg.Listen}
	}
	addrs := []string{}
	if tsIP, ok := findTailscaleIP(); ok {
		addrs = append(addrs, fmt.Sprintf("%s:%d", tsIP, cfg.Port))
		log.Printf("Tailscale IP detected: %s", tsIP)
		log.Printf("iPhone Shortcuts URL: http://%s:%d/clip", tsIP, cfg.Port)
	} else {
		log.Printf("WARNING: no Tailscale IP (100.64.0.0/10) found; listening on localhost only")
	}
	addrs = append(addrs, fmt.Sprintf("127.0.0.1:%d", cfg.Port))
	return addrs
}

// guardian watches the local clipboard and the box. All clipboard calls run
// on this single OS-thread-locked goroutine (required by Win32).
func guardian(box *Box, logger *log.Logger) {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()

	lastSeq := clipboardSeq()
	var lastAppliedVersion int64 = 0
	var lastClearGen int64 = 0
	// If the box already holds an iPhone item at startup, apply it once.
	ticker := time.NewTicker(500 * time.Millisecond)
	defer ticker.Stop()
	for range ticker.C {
		// 0) Box was cleared from the page/API: forget what we applied so
		// the next iPhone item is treated as new.
		if g := box.clearGeneration(); g != lastClearGen {
			lastClearGen = g
			lastAppliedVersion = 0
			logger.Printf("box cleared; guardian state reset")
		}
		// 1) Box -> local clipboard (iPhone-origin, not yet applied).
		if e := box.snapshot(); e != nil && e.Origin == "iphone" && e.Version != lastAppliedVersion {
			c := &clipContent{Type: e.Type, ContentHash: e.ContentHash}
			if e.Type == "text" {
				c.Text = string(e.Data)
			} else {
				c.PNG = e.Data
			}
			if err := writeClipboard(c); err != nil {
				logger.Printf("apply to clipboard failed: %v", err)
			} else {
				lastAppliedVersion = e.Version
				lastSeq = clipboardSeq() // mark our own write as seen
				logger.Printf("applied box v%d (%s) to Windows clipboard", e.Version, e.Type)
			}
		}
		// 2) Local clipboard -> box (user copied something new).
		seq := clipboardSeq()
		if seq != lastSeq {
			lastSeq = seq
			c, ok, err := readClipboard()
			if err != nil {
				logger.Printf("read clipboard failed: %v", err)
				continue
			}
			if !ok || c == nil {
				continue // unsupported format (files etc.) or empty
			}
			if e := box.snapshot(); e != nil && e.ContentHash == c.ContentHash {
				continue // identical to what's in the box; do not loop
			}
			var data []byte
			var mime string
			if c.Type == "text" {
				data = []byte(c.Text)
				mime = "text/plain"
			} else {
				data = c.PNG
				mime = "image/png"
			}
			e, stored := box.store(c.Type, data, mime, "windows", c.ContentHash)
			if stored {
				logger.Printf("stored Windows clipboard as box v%d (%s)", e.Version, e.Type)
			}
		}
	}
}

func main() {
	configPath := flag.String("config", "", "path to clipshare.json (default: next to exe)")
	listenOverride := flag.String("listen", "", "override listen address, e.g. 127.0.0.1:18765")
	flag.Parse()

	dir := exeDir()
	cfgPath := *configPath
	if cfgPath == "" {
		cfgPath = filepath.Join(dir, "clipshare.json")
	}
	cfg, err := loadOrCreateConfig(cfgPath)
	if err != nil {
		fmt.Fprintln(os.Stderr, "config error:", err)
		os.Exit(1)
	}
	if *listenOverride != "" {
		cfg.Listen = *listenOverride
	}

	// Log to console and to clipshare.log next to the exe.
	logFile, err := os.OpenFile(filepath.Join(dir, "clipshare.log"), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	var logOut io.Writer = os.Stdout
	if err == nil {
		defer logFile.Close()
		// File first: in a windowless (GUI subsystem) build stdout is
		// invalid, and MultiWriter stops at the first failing writer.
		logOut = io.MultiWriter(logFile, os.Stdout)
	}
	logger := log.New(logOut, "", log.LstdFlags)
	log.SetOutput(logOut)
	log.SetFlags(log.LstdFlags)

	box := &Box{}
	handler := newMux(box, cfg.Token)

	logger.Printf("clipshare starting (config: %s)", cfgPath)
	logger.Printf("token: %s", cfg.Token)
	logger.Printf("enter this token in the iPhone Shortcuts (see manual)")

	go guardian(box, logger)

	addrs := listenAddrs(cfg)
	errCh := make(chan error, len(addrs))
	for _, addr := range addrs {
		ln, err := net.Listen("tcp", addr)
		if err != nil {
			logger.Printf("listen %s failed: %v", addr, err)
			continue
		}
		logger.Printf("listening on http://%s", addr)
		srv := &http.Server{Handler: handler}
		go func() { errCh <- srv.Serve(ln) }()
	}
	if len(addrs) == 0 {
		logger.Fatalf("no listener could be started")
	}
	err = <-errCh
	logger.Fatalf("server stopped: %v", err)
}
