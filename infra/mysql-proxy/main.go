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
)

type MySQLProxy struct {
	mu         sync.RWMutex
	activeHost string
	activePort string
	primary    string
	replica    string
	healthTick time.Duration
}

func NewMySQLProxy(primaryHost, primaryPort, replicaHost, replicaPort string, healthTick time.Duration) *MySQLProxy {
	return &MySQLProxy{
		activeHost: primaryHost,
		activePort: primaryPort,
		primary:    primaryHost + ":" + primaryPort,
		replica:    replicaHost + ":" + replicaPort,
		healthTick: healthTick,
	}
}

func (p *MySQLProxy) GetActive() (string, string) {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.activeHost, p.activePort
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

func (p *MySQLProxy) checkMySQL(host, port string) bool {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
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
		activeHost, activePort := p.GetActive()

		if p.checkMySQL(activeHost, activePort) {
			continue
		}

		other := p.replica
		if activeHost+":"+activePort == p.replica {
			other = p.primary
		}

		if p.checkMySQL(activeHost, activePort) {
			continue
		}

		// Parse other host:port
		h, port, _ := net.SplitHostPort(other)
		if h != "" {
			p.SwitchTarget(h, port)
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
		p.SwitchTarget(req.Host, req.Port)
	}
	w.WriteHeader(http.StatusOK)
	fmt.Fprintf(w, "switched to %s:%s", p.activeHost, p.activePort)
}

func (p *MySQLProxy) handleStatus(w http.ResponseWriter, r *http.Request) {
	host, port := p.GetActive()
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{
		"active_host": host,
		"active_port": port,
		"primary":     p.primary,
		"replica":     p.replica,
	})
}

func main() {
	proxyPort := os.Getenv("PROXY_PORT")
	if proxyPort == "" {
		proxyPort = "3308"
	}

	proxy := NewMySQLProxy(
		"127.0.0.1", "3306",
		"127.0.0.1", "3307",
		5*time.Second,
	)

	go proxy.healthCheckLoop()

	addr := fmt.Sprintf("0.0.0.0:%s", proxyPort)
	listener, err := net.Listen("tcp", addr)
	if err != nil {
		log.Fatalf("[proxy] listen %s: %v", addr, err)
	}
	log.Printf("[proxy] MySQL proxy listening on %s → %s:%s", addr, "127.0.0.1", "3306")

	mux := http.NewServeMux()
	mux.HandleFunc("/switch", proxy.handleSwitch)
	mux.HandleFunc("/status", proxy.handleStatus)
	go func() {
		log.Printf("[proxy] management API on :8081")
		http.ListenAndServe(":8081", mux)
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
