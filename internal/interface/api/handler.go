package api

import (
	"net/http"

	"github.com/dbs-rtf/agent/internal/engine"
	"github.com/dbs-rtf/agent/internal/interface/nlp"
	"github.com/dbs-rtf/agent/internal/monitor"
	"github.com/dbs-rtf/agent/internal/operations"
	"github.com/dbs-rtf/agent/pkg/model"
	"github.com/gin-gonic/gin"
)

type Server struct {
	router       *gin.Engine
	orchestrator *engine.Orchestrator
	nlpParser    *nlp.IntentParser
	defaultUser  engine.User
	supervisor   *monitor.Supervisor
}

func NewServer(orchestrator *engine.Orchestrator, supervisor *monitor.Supervisor) *Server {
	gin.SetMode(gin.ReleaseMode)
	r := gin.New()
	r.Use(gin.Recovery())

	s := &Server{
		router:       r,
		orchestrator: orchestrator,
		defaultUser:  engine.User{Name: "api-user", Role: operations.RoleAdmin},
		supervisor:   supervisor,
	}

	s.setupRoutes()
	return s
}

func (s *Server) SetNLPParser(parser *nlp.IntentParser) {
	s.nlpParser = parser
}

func (s *Server) setupRoutes() {
	s.router.POST("/execute", s.handleExecute)
	s.router.POST("/nlp", s.handleNLP)
	s.router.GET("/plugins", s.handleListPlugins)
	s.router.GET("/health", s.handleHealth)

	if s.supervisor != nil {
		mh := NewMonitorHandler(s.supervisor)
		s.router.GET("/monitor/status", mh.GetStatus)
		s.router.GET("/monitor/events", mh.GetEvents)
		s.router.POST("/monitor/pause", mh.Pause)
		s.router.POST("/monitor/resume", mh.Resume)
	}
}

type ExecuteRequest struct {
	Plugin     string               `json:"plugin" binding:"required"`
	Operation  string               `json:"operation" binding:"required"`
	Instance   model.InstanceConfig `json:"instance" binding:"required"`
	Params     map[string]string    `json:"params"`
	DryRun     bool                 `json:"dry_run"`
}

type NLRequest struct {
	Query    string               `json:"query" binding:"required"`
	Instance model.InstanceConfig `json:"instance"`
}

func (s *Server) handleExecute(c *gin.Context) {
	var req ExecuteRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	cmd := model.Command{
		Plugin:    req.Plugin,
		Operation: req.Operation,
		Instance:  req.Instance,
		Params:    req.Params,
		DryRun:    req.DryRun,
	}

	result, err := s.orchestrator.Execute(c.Request.Context(), cmd, s.defaultUser, "api")
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"success":  result.Success,
		"data":     result.Data,
		"output":   result.RawOutput,
		"warnings": result.Warnings,
		"duration": result.Duration.String(),
	})
}

func (s *Server) handleNLP(c *gin.Context) {
	var req NLRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	if s.nlpParser == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "NLP not configured"})
		return
	}

	intent, err := s.nlpParser.Parse(c.Request.Context(), req.Query)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	cmd := s.nlpParser.ToCommand(intent)
	if req.Instance.Host != "" {
		cmd.Instance = req.Instance
	}

	result, err := s.orchestrator.Execute(c.Request.Context(), cmd, s.defaultUser, "api")
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"intent":   intent,
		"success":  result.Success,
		"data":     result.Data,
		"output":   result.RawOutput,
		"warnings": result.Warnings,
		"duration": result.Duration.String(),
	})
}

func (s *Server) handleListPlugins(c *gin.Context) {
	plugins := []gin.H{}
	for _, p := range operations.List() {
		plugins = append(plugins, gin.H{
			"name":        p.Name(),
			"description": p.Description(),
			"operations":  p.SupportedOps(),
		})
	}
	c.JSON(http.StatusOK, gin.H{"plugins": plugins})
}

func (s *Server) handleHealth(c *gin.Context) {
	c.JSON(http.StatusOK, gin.H{"status": "ok"})
}

func (s *Server) Run(addr string) error {
	return s.router.Run(addr)
}

func (s *Server) Router() *gin.Engine {
	return s.router
}
