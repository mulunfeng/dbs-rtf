package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"sync"
	"time"

	_ "github.com/go-sql-driver/mysql"
	"gopkg.in/yaml.v3"
)

type ProxyConfig struct {
	ListenPort     int            `yaml:"listen_port"`
	ManagementPort int            `yaml:"management_port"`
	InitialMaster  string         `yaml:"initial_master"`
	HostnameMap    map[string]string `yaml:"hostname_map"`
}

func loadConfig(path string) (*ProxyConfig, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var cfg ProxyConfig
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return nil, err
	}
	if cfg.ListenPort == 0 {
		cfg.ListenPort = 3309
	}
	if cfg.ManagementPort == 0 {
		cfg.ManagementPort = 8081
	}
	if cfg.InitialMaster == "" {
		cfg.InitialMaster = "127.0.0.1:3306"
	}
	return &cfg, nil
}

// MySQLProxy is a simple TCP proxy for MySQL with HTTP switch API.
type MySQLProxy struct {
	mu           sync.RWMutex
	activeHost   string // resolved host address (e.g., 127.0.0.1)
	activePort   string // resolved port (e.g., 3307)
	hostnameMap  map[string]string // container_name -> host:port
	backendAddrs map[string]string // container_name -> address (stable)
	backendHealth map[string]bool  // container_name -> healthy
	healthTick   time.Duration
}

func NewMySQLProxy(cfg *ProxyConfig, healthTick time.Duration) *MySQLProxy {
	host, port := parseHostPort(cfg.InitialMaster)
	proxy := &MySQLProxy{
		activeHost:    host,
		activePort:    port,
		hostnameMap:   cfg.HostnameMap,
		backendAddrs:  make(map[string]string),
		backendHealth: make(map[string]bool),
		healthTick:    healthTick,
	}
	// Initialize backends from hostname map
	for name, addr := range cfg.HostnameMap {
		proxy.backendAddrs[name] = addr
	}
	return proxy
}

// SwitchByContainerName switches the proxy target using a container name
// and the internal port (3306).
func (p *MySQLProxy) SwitchByContainerName(containerName, port string) {
	addrKey := containerName + ":" + port
	p.mu.RLock()
	resolved, ok := p.hostnameMap[containerName]
	p.mu.RUnlock()
	if !ok {
		log.Printf("[proxy] WARNING: container name %q not found in hostname_map, using as-is", containerName)
		resolved = addrKey
	}
	host, hostPort := parseHostPort(resolved)
	p.SwitchTarget(host, hostPort)
}

func (p *MySQLProxy) SwitchTarget(host, port string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.activeHost != host || p.activePort != port {
		old := p.activeHost + ":" + p.activePort
		new := host + ":" + port
		log.Printf("[proxy] SWITCHING target: %s -> %s", old, new)
		p.activeHost = host
		p.activePort = port
	}
}

func (p *MySQLProxy) GetActive() (string, string) {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.activeHost, p.activePort
}

func (p *MySQLProxy) checkMySQL(addr string) bool {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	host, port := parseHostPort(addr)
	dsn := fmt.Sprintf("root:rootpass123@tcp(%s:%s)/mysql?timeout=3s", host, port)
	db, err := sql.Open("mysql", dsn)
	if err != nil {
		return false
	}
	defer db.Close()
	return db.PingContext(ctx) == nil
}

func (p *MySQLProxy) healthCheckLoop() {
	ticker := time.NewTicker(p.healthTick)
	defer ticker.Stop()

	for range ticker.C {
		p.mu.RLock()
		activeHost := p.activeHost
		activePort := p.activePort
		backendAddrs := make(map[string]string)
		for k, v := range p.backendAddrs {
			backendAddrs[k] = v
		}
		p.mu.RUnlock()

		active := activeHost + ":" + activePort
		activeHealthy := p.checkMySQL(active)
		if !activeHealthy {
			log.Printf("[proxy] HEALTH CHECK FAILED: active target %s is unreachable", active)
		}

		// Health status per backend
		for name, addr := range backendAddrs {
			healthy := p.checkMySQL(addr)
			p.mu.Lock()
			p.backendHealth[name] = healthy
			p.mu.Unlock()
		}
	}
}

func (p *MySQLProxy) handleClient(conn net.Conn) {
	defer conn.Close()

	host, port := p.GetActive()
	target, err := net.DialTimeout("tcp", host+":"+port, 5*time.Second)
	if err != nil {
		time.Sleep(200 * time.Millisecond)
		host, port = p.GetActive()
		target, err = net.DialTimeout("tcp", host+":"+port, 5*time.Second)
		if err != nil {
			return
		}
	}
	defer target.Close()

	done := make(chan struct{}, 2)
	go func() {
		_, _ = ioCopy(target, conn)
		done <- struct{}{}
	}()
	go func() {
		_, _ = ioCopy(conn, target)
		done <- struct{}{}
	}()

	<-done
}

func ioCopy(dst net.Conn, src net.Conn) (int64, error) {
	buf := make([]byte, 32*1024)
	var total int64
	for {
		n, err := src.Read(buf)
		if n > 0 {
			wn, werr := dst.Write(buf[:n])
			total += int64(wn)
			if werr != nil {
				return total, werr
			}
		}
		if err != nil {
			return total, err
		}
	}
}

func (p *MySQLProxy) handleSwitch(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "POST only", http.StatusMethodNotAllowed)
		return
	}
	var req struct {
		Host string `json:"host"`
		Port string `json:"port"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if req.Host != "" && req.Port != "" {
		p.SwitchByContainerName(req.Host, req.Port)
	}
	w.WriteHeader(http.StatusOK)
	fmt.Fprintf(w, "switched to %s:%s", p.activeHost, p.activePort)
}

func (p *MySQLProxy) handleStatus(w http.ResponseWriter, r *http.Request) {
	host, port := p.GetActive()
	p.mu.RLock()
	backends := make(map[string]any)
	for name, addr := range p.backendAddrs {
		backends[name] = map[string]any{
			"address": addr,
			"healthy": p.backendHealth[name],
		}
	}
	p.mu.RUnlock()

	status := map[string]any{
		"active_host": host,
		"active_port": port,
		"backends":    backends,
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(status)
}

func parseHostPort(addr string) (host, port string) {
	host = addr
	port = "3306"
	for i := len(addr) - 1; i >= 0; i-- {
		if addr[i] == ':' {
			host = addr[:i]
			port = addr[i+1:]
			break
		}
	}
	return
}

func main() {
	configPath := os.Getenv("PROXY_CONFIG")
	if configPath == "" {
		configPath = "../../configs/mysql-proxy.yaml"
	}

	cfg, err := loadConfig(configPath)
	if err != nil {
		log.Printf("[proxy] warning: could not load config: %v, using defaults", err)
		cfg = &ProxyConfig{
			ListenPort:     3309,
			ManagementPort: 8081,
			InitialMaster:  "127.0.0.1:3306",
			HostnameMap: map[string]string{
				"mysql-primary": "127.0.0.1:3306",
				"mysql-replica": "127.0.0.1:3307",
			},
		}
	}

	proxy := NewMySQLProxy(cfg, 5*time.Second)

	go proxy.healthCheckLoop()

	addr := fmt.Sprintf("0.0.0.0:%d", cfg.ListenPort)
	listener, err := net.Listen("tcp", addr)
	if err != nil {
		log.Fatalf("[proxy] listen %s: %v", addr, err)
	}
	log.Printf("[proxy] MySQL proxy listening on %s → %s", addr, cfg.InitialMaster)

	mux := http.NewServeMux()
	mux.HandleFunc("/switch", proxy.handleSwitch)
	mux.HandleFunc("/status", proxy.handleStatus)
	go func() {
		mgmtAddr := fmt.Sprintf(":%d", cfg.ManagementPort)
		log.Printf("[proxy] management API on %s", mgmtAddr)
		if err := http.ListenAndServe(mgmtAddr, mux); err != nil {
			log.Printf("[proxy] management API error: %v", err)
		}
	}()

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()

	go func() {
		for {
			conn, err := listener.Accept()
			if err != nil {
				select {
				case <-ctx.Done():
					return
				default:
					log.Printf("[proxy] accept: %v", err)
					continue
				}
			}
			go proxy.handleClient(conn)
		}
	}()

	<-ctx.Done()
	log.Println("[proxy] shutting down")
	listener.Close()
}
