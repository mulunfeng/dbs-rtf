package main

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/exec"
	"sync"
)

type FailoverEvent struct {
	EventType string `json:"EventType"`
	From      string `json:"From"`
	To        string `json:"To"`
	Reason    string `json:"Reason"`
	Result    string `json:"Result"`
}

type VIPManager struct {
	mu          sync.Mutex
	vip         string
	iface       string
	currentPeer string
}

func NewVIPManager(vip, iface, currentPeer string) *VIPManager {
	return &VIPManager{
		vip:         vip,
		iface:       iface,
		currentPeer: currentPeer,
	}
}

// assignVIP adds the VIP to this machine's interface
func (m *VIPManager) assignVIP() error {
	cmd := exec.Command("ip", "addr", "add", m.vip, "dev", m.iface)
	return cmd.Run()
}

// removeVIP removes the VIP from this machine's interface
func (m *VIPManager) removeVIP() error {
	cmd := exec.Command("ip", "addr", "del", m.vip, "dev", m.iface)
	return cmd.Run()
}

func (m *VIPManager) handleWebhook(w http.ResponseWriter, r *http.Request) {
	var event FailoverEvent
	if err := json.NewDecoder(r.Body).Decode(&event); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	if event.EventType == "failover_complete" {
		// This node is the new master — assign VIP
		if err := m.assignVIP(); err != nil {
			log.Printf("[vip] failed to assign VIP: %v", err)
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		log.Printf("[vip] VIP %s assigned to %s", m.vip, m.iface)
	}

	if event.Result == "failed" || event.EventType == "instance_down" {
		// This node lost — remove VIP so failover can proceed
		m.removeVIP()
		log.Printf("[vip] VIP %s removed from %s", m.vip, m.iface)
	}

	w.WriteHeader(http.StatusOK)
	fmt.Fprintf(w, "VIP action taken for event: %s", event.EventType)
}

func (m *VIPManager) handleStatus(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{
		"vip":    m.vip,
		"iface":  m.iface,
		"peer":   m.currentPeer,
		"status": "active",
	})
}

func main() {
	vip := os.Getenv("VIP_ADDRESS")
	if vip == "" {
		vip = "10.0.1.100/24"
	}

	iface := os.Getenv("VIP_INTERFACE")
	if iface == "" {
		iface = "eth0"
	}

	peer := os.Getenv("VIP_PEER_IP")

	mgr := NewVIPManager(vip, iface, peer)

	http.HandleFunc("/webhook", mgr.handleWebhook)
	http.HandleFunc("/status", mgr.handleStatus)

	addr := ":9090"
	log.Printf("[vip] starting VIP manager on %s (VIP=%s iface=%s)", addr, vip, iface)
	if err := http.ListenAndServe(addr, nil); err != nil {
		log.Fatalf("[vip] failed: %v", err)
	}
}
