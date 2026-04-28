package api

import (
	"net/http"

	"github.com/dbs-rtf/agent/internal/monitor"
	"github.com/gin-gonic/gin"
)

type MonitorHandler struct {
	supervisor *monitor.Supervisor
}

func NewMonitorHandler(supervisor *monitor.Supervisor) *MonitorHandler {
	return &MonitorHandler{supervisor: supervisor}
}

func (h *MonitorHandler) GetStatus(c *gin.Context) {
	if h.supervisor == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "monitor not enabled"})
		return
	}

	status := h.supervisor.GetAllStatus()
	events := h.supervisor.GetEvents()

	c.JSON(http.StatusOK, gin.H{
		"instances": status,
		"recent_events": events,
	})
}

func (h *MonitorHandler) GetEvents(c *gin.Context) {
	if h.supervisor == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "monitor not enabled"})
		return
	}

	events := h.supervisor.GetEvents()
	c.JSON(http.StatusOK, gin.H{"events": events})
}

func (h *MonitorHandler) Pause(c *gin.Context) {
	if h.supervisor == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "monitor not enabled"})
		return
	}

	h.supervisor.Pause()
	c.JSON(http.StatusOK, gin.H{"status": "paused"})
}

func (h *MonitorHandler) Resume(c *gin.Context) {
	if h.supervisor == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "monitor not enabled"})
		return
	}

	h.supervisor.Resume()
	c.JSON(http.StatusOK, gin.H{"status": "resumed"})
}
