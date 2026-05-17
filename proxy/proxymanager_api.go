package proxy

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/mostlygeek/llama-swap/event"
	"github.com/mostlygeek/llama-swap/internal/audit"
	"github.com/mostlygeek/llama-swap/internal/perf"
	"github.com/mostlygeek/llama-swap/proxy/codex"
)

type Model struct {
	Id          string   `json:"id"`
	Name        string   `json:"name"`
	Description string   `json:"description"`
	State       string   `json:"state"`
	Unlisted    bool     `json:"unlisted"`
	PeerID      string   `json:"peerID"`
	Aliases     []string `json:"aliases,omitempty"`
}

func addApiHandlers(pm *ProxyManager) {
	// Add API endpoints for React to consume
	// Protected with API key authentication
	// Keys with model restrictions are blocked from UI management endpoints
	apiGroup := pm.ginEngine.Group("/api", pm.apiKeyAuth(), pm.blockRestrictedKeys())
	{
		apiGroup.POST("/models/unload", pm.apiUnloadAllModels)
		apiGroup.POST("/models/unload/*model", pm.apiUnloadSingleModelHandler)
		apiGroup.GET("/events", pm.apiSendEvents)
		apiGroup.GET("/metrics", pm.apiGetMetrics)
		apiGroup.GET("/performance", pm.apiGetPerformance)
		apiGroup.GET("/version", pm.apiGetVersion)
		apiGroup.GET("/captures/:id", pm.apiGetCapture)
		if pm.auditStore != nil {
			apiGroup.GET("/audit/users", pm.apiAuditUsers)
			apiGroup.GET("/audit/usage", pm.apiAuditUsage)
			apiGroup.GET("/audit/usage/:user_id", pm.apiAuditUserUsage)
			apiGroup.GET("/audit/captures", pm.apiAuditCaptures)
			apiGroup.GET("/audit/activity", pm.apiAuditActivity)
		}
		apiGroup.GET("/codex/accounts", pm.apiCodexAccounts)
		if pm.auditStore != nil {
			apiGroup.GET("/codex/usage", pm.apiCodexUsage)
			apiGroup.GET("/codex/usage/:user_id", pm.apiCodexUserUsage)
		}
	}
}

type codexAccountInfo struct {
	Name        string                              `json:"name"`
	PlanType    string                              `json:"plan_type,omitempty"`
	ActiveLimit string                              `json:"active_limit,omitempty"`
	TokenValid  bool                                `json:"token_valid"`
	ExpiresAt   int64                               `json:"token_expires_at"`
	AccountID   string                              `json:"account_id,omitempty"`
	Primary     codex.WindowSnapshot                `json:"primary"`
	Secondary   codex.WindowSnapshot                `json:"secondary"`
	Credits     codex.CreditsSnapshot               `json:"credits"`
	Stats       *codex.AccountStatsSnapshot         `json:"stats,omitempty"`
	ModelLimits map[string]codex.ModelLimitSnapshot `json:"model_limits,omitempty"`
}

func (pm *ProxyManager) apiUnloadAllModels(c *gin.Context) {
	pm.StopProcesses(StopImmediately)
	c.JSON(http.StatusOK, gin.H{"msg": "ok"})
}

func (pm *ProxyManager) getModelStatus() []Model {
	// Extract keys and sort them
	models := []Model{}

	modelIDs := make([]string, 0, len(pm.config.Models))
	for modelID := range pm.config.Models {
		modelIDs = append(modelIDs, modelID)
	}
	sort.Strings(modelIDs)

	// Iterate over sorted keys
	for _, modelID := range modelIDs {
		// Get process state
		state := "unknown"
		var process *Process
		if pm.matrix != nil {
			process, _ = pm.matrix.GetProcess(modelID)
		} else {
			processGroup := pm.findGroupByModelName(modelID)
			if processGroup != nil {
				process = processGroup.processes[modelID]
			}
		}
		if process != nil {
			switch process.CurrentState() {
			case StateReady:
				state = "ready"
			case StateStarting:
				state = "starting"
			case StateStopping:
				state = "stopping"
			case StateShutdown:
				state = "shutdown"
			case StateStopped:
				state = "stopped"
			}
		}
		models = append(models, Model{
			Id:          modelID,
			Name:        pm.config.Models[modelID].Name,
			Description: pm.config.Models[modelID].Description,
			State:       state,
			Unlisted:    pm.config.Models[modelID].Unlisted,
			Aliases:     pm.config.Models[modelID].Aliases,
		})
	}

	// Iterate over the peer models
	if pm.peerProxy != nil {
		for peerID, peer := range pm.peerProxy.ListPeers() {
			peerPrefix := pm.config.PrefixPeerModels
			if peer.PrefixPeerModels != nil {
				peerPrefix = *peer.PrefixPeerModels
			}
			for _, modelID := range peer.Models {
				displayID := modelID
				if peerPrefix {
					displayID = peerID + "/" + modelID
				}
				models = append(models, Model{
					Id:     displayID,
					PeerID: peerID,
				})
			}
		}
	}

	return models
}

type messageType string

const (
	msgTypeModelStatus messageType = "modelStatus"
	msgTypeLogData     messageType = "logData"
	msgTypeMetrics     messageType = "metrics"
	msgTypeInFlight    messageType = "inflight"
	msgTypeCodexQuota  messageType = "codexQuota"
)

type messageEnvelope struct {
	Type messageType `json:"type"`
	Data string      `json:"data"`
}

// sends a stream of different message types that happen on the server
func (pm *ProxyManager) apiSendEvents(c *gin.Context) {
	c.Header("Content-Type", "text/event-stream")
	c.Header("Cache-Control", "no-cache")
	c.Header("Connection", "keep-alive")
	c.Header("X-Content-Type-Options", "nosniff")
	// prevent nginx from buffering SSE
	c.Header("X-Accel-Buffering", "no")

	sendBuffer := make(chan messageEnvelope, 25)
	ctx, cancel := context.WithCancel(c.Request.Context())
	sendModels := func() {
		data, err := json.Marshal(pm.getModelStatus())
		if err == nil {
			msg := messageEnvelope{Type: msgTypeModelStatus, Data: string(data)}
			select {
			case sendBuffer <- msg:
			case <-ctx.Done():
				return
			default:
			}

		}
	}

	sendLogData := func(source string, data []byte) {
		data, err := json.Marshal(gin.H{
			"source": source,
			"data":   string(data),
		})
		if err == nil {
			select {
			case sendBuffer <- messageEnvelope{Type: msgTypeLogData, Data: string(data)}:
			case <-ctx.Done():
				return
			default:
			}
		}
	}

	sendMetrics := func(metrics []ActivityLogEntry) {
		jsonData, err := json.Marshal(metrics)
		if err == nil {
			select {
			case sendBuffer <- messageEnvelope{Type: msgTypeMetrics, Data: string(jsonData)}:
			case <-ctx.Done():
				return
			default:
			}
		}
	}

	sendInFlight := func(total int) {
		jsonData, err := json.Marshal(gin.H{"total": total})
		if err == nil {
			select {
			case sendBuffer <- messageEnvelope{Type: msgTypeInFlight, Data: string(jsonData)}:
			case <-ctx.Done():
				return
			default:
			}
		}
	}

	sendCodexQuota := func() {
		if pm.peerProxy == nil {
			return
		}
		codexProxies := pm.peerProxy.GetCodexProxies()
		if len(codexProxies) == 0 {
			return
		}
		allQuotas := make(map[string]codex.QuotaSnapshot)
		for _, proxy := range codexProxies {
			if proxy == nil {
				continue
			}
			for name, snapshot := range proxy.QuotaStats() {
				allQuotas[name] = snapshot
			}
		}
		data, err := json.Marshal(allQuotas)
		if err == nil {
			select {
			case sendBuffer <- messageEnvelope{Type: msgTypeCodexQuota, Data: string(data)}:
			case <-ctx.Done():
				return
			default:
			}
		}
	}

	/**
	 * Send updated models list
	 */
	defer event.On(func(e ProcessStateChangeEvent) {
		sendModels()
	})()
	defer event.On(func(e ConfigFileChangedEvent) {
		sendModels()
	})()

	/**
	 * Send Log data
	 */
	defer pm.proxyLogger.OnLogData(func(data []byte) {
		sendLogData("proxy", data)
	})()
	defer pm.upstreamLogger.OnLogData(func(data []byte) {
		sendLogData("upstream", data)
	})()

	/**
	 * Send Metrics data
	 */
	defer event.On(func(e ActivityLogEvent) {
		sendMetrics([]ActivityLogEntry{e.Metrics})
	})()

	/**
	 * Send in-flight request stats related to token stats "Waiting: N" count.
	 */
	defer event.On(func(e InFlightRequestsEvent) {
		sendInFlight(e.Total)
	})()

	go func() {
		sendCodexQuota()
		ticker := time.NewTicker(10 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-ticker.C:
				sendCodexQuota()
			case <-ctx.Done():
				return
			}
		}
	}()

	// send initial batch of data
	sendLogData("proxy", pm.proxyLogger.GetHistory())
	sendLogData("upstream", pm.upstreamLogger.GetHistory())
	sendModels()
	sendMetrics(pm.metricsMonitor.getMetrics())
	sendInFlight(pm.inFlightCounter.Current())

	for {
		select {
		case <-c.Request.Context().Done():
			cancel()
			return
		case <-pm.shutdownCtx.Done():
			cancel()
			return
		case msg := <-sendBuffer:
			c.SSEvent("message", msg)
			c.Writer.Flush()
		}
	}
}

func (pm *ProxyManager) apiGetMetrics(c *gin.Context) {
	jsonData, err := pm.metricsMonitor.getMetricsJSON()
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to get metrics"})
		return
	}
	c.Data(http.StatusOK, "application/json", jsonData)
}

func (pm *ProxyManager) prometheusMetricsHandler(c *gin.Context) {
	if pm.perfMonitor == nil {
		c.String(http.StatusServiceUnavailable, "# performance monitor not available\n")
		return
	}
	pm.perfMonitor.MetricsHandler().ServeHTTP(c.Writer, c.Request)
}

func (pm *ProxyManager) apiGetPerformance(c *gin.Context) {
	if pm.perfMonitor == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "performance monitor not available"})
		return
	}

	sysStats, gpuStats := pm.perfMonitor.Current()

	var after time.Time
	if afterStr := c.Query("after"); afterStr != "" {
		ts, err := time.Parse(time.RFC3339, afterStr)
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "invalid 'after' timestamp, use RFC3339 format"})
			return
		}
		after = ts
	}

	if !after.IsZero() {
		filtered := make([]perf.SysStat, 0, len(sysStats))
		for _, s := range sysStats {
			if s.Timestamp.After(after) {
				filtered = append(filtered, s)
			}
		}
		sysStats = filtered

		filteredGpu := make([]perf.GpuStat, 0, len(gpuStats))
		for _, g := range gpuStats {
			if g.Timestamp.After(after) {
				filteredGpu = append(filteredGpu, g)
			}
		}
		gpuStats = filteredGpu
	}

	c.JSON(http.StatusOK, gin.H{
		"sys_stats": sysStats,
		"gpu_stats": gpuStats,
	})
}

func (pm *ProxyManager) apiUnloadSingleModelHandler(c *gin.Context) {
	requestedModel := strings.TrimPrefix(c.Param("model"), "/")
	realModelName, found := pm.config.RealModelName(requestedModel)
	if !found {
		pm.sendErrorResponse(c, http.StatusNotFound, "Model not found")
		return
	}

	if !pm.isModelAllowedForContext(c, requestedModel) {
		pm.sendErrorResponse(c, http.StatusForbidden, fmt.Sprintf("API key not authorized for model: %s", requestedModel))
		return
	}

	var stopErr error
	if pm.matrix != nil {
		stopErr = pm.matrix.StopProcess(realModelName, StopImmediately)
	} else {
		processGroup := pm.findGroupByModelName(realModelName)
		if processGroup == nil {
			pm.sendErrorResponse(c, http.StatusInternalServerError, fmt.Sprintf("process group not found for model %s", requestedModel))
			return
		}
		stopErr = processGroup.StopProcess(realModelName, StopImmediately)
	}

	if stopErr != nil {
		pm.sendErrorResponse(c, http.StatusInternalServerError, fmt.Sprintf("error stopping process: %s", stopErr.Error()))
		return
	}
	c.String(http.StatusOK, "OK")
}

func (pm *ProxyManager) apiGetVersion(c *gin.Context) {
	c.JSON(http.StatusOK, map[string]string{
		"version":    pm.version,
		"commit":     pm.commit,
		"build_date": pm.buildDate,
	})
}

func (pm *ProxyManager) apiGetCapture(c *gin.Context) {
	idStr := c.Param("id")
	id, err := strconv.Atoi(idStr)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid capture ID"})
		return
	}

	// Check in-memory captures first to avoid stale audit data from metric_id
	// collisions (metric IDs reset to 0 on each process restart).
	capture := pm.metricsMonitor.getCaptureByID(id)
	if capture != nil && !(capture.ReqPath == "" && capture.ReqHeaders == nil && capture.ReqBody == nil && capture.RespHeaders == nil && capture.RespBody == nil) {
		inferCaptureContentTypes(capture)
		jsonBytes, err := json.Marshal(capture)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to marshal capture"})
			return
		}
		c.Data(http.StatusOK, "application/json", jsonBytes)
		return
	}

	// Fall back to audit store for historical captures
	if pm.auditStore != nil {
		data, err := pm.auditStore.GetCaptureByMetricID(int64(id))
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to get capture"})
			return
		}
		if data == nil {
			c.JSON(http.StatusNotFound, gin.H{"error": "capture not found"})
			return
		}
		capture, err := decompressCapture(data)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to decompress capture"})
			return
		}
		inferCaptureContentTypes(capture)
		jsonBytes, err := json.Marshal(capture)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to marshal capture"})
			return
		}
		c.Data(http.StatusOK, "application/json", jsonBytes)
		return
	}

	c.JSON(http.StatusNotFound, gin.H{"error": "capture not found"})
}

func (pm *ProxyManager) apiCodexAccounts(c *gin.Context) {
	if pm.peerProxy == nil {
		c.JSON(http.StatusOK, []interface{}{})
		return
	}

	codexProxies := pm.peerProxy.GetCodexProxies()
	if len(codexProxies) == 0 {
		c.JSON(http.StatusOK, []interface{}{})
		return
	}

	accounts := collectCodexAccounts(codexProxies)
	c.JSON(http.StatusOK, accounts)
}

func collectCodexAccounts(codexProxies map[string]*codex.Proxy) []codexAccountInfo {
	accounts := make(map[string]*codexAccountInfo)
	quotaCapturedAt := make(map[string]time.Time)
	ensureAccount := func(name string) *codexAccountInfo {
		account := accounts[name]
		if account == nil {
			account = &codexAccountInfo{Name: name}
			accounts[name] = account
		}
		return account
	}

	for _, proxy := range codexProxies {
		if proxy == nil {
			continue
		}

		for _, status := range proxy.ListAccountStatuses() {
			if status.Name == "" {
				continue
			}
			account := ensureAccount(status.Name)
			account.TokenValid = account.TokenValid || status.TokenValid
			if status.ExpiresAt > account.ExpiresAt {
				account.ExpiresAt = status.ExpiresAt
			}
			if account.AccountID == "" {
				account.AccountID = status.AccountID
			}
		}

		for name, quota := range proxy.QuotaStats() {
			if name == "" {
				continue
			}
			if capturedAt, ok := quotaCapturedAt[name]; ok && !quota.CapturedAt.After(capturedAt) {
				continue
			}
			account := ensureAccount(name)
			account.PlanType = quota.PlanType
			account.ActiveLimit = quota.ActiveLimit
			account.Primary = quota.Primary
			account.Secondary = quota.Secondary
			account.Credits = quota.Credits
			account.ModelLimits = quota.ModelLimits
			quotaCapturedAt[name] = quota.CapturedAt
		}

		for _, stats := range proxy.Stats() {
			if stats.Name == "" {
				continue
			}
			account := ensureAccount(stats.Name)
			if account.Stats == nil {
				statsCopy := stats
				account.Stats = &statsCopy
				continue
			}
			account.Stats.TotalReqs += stats.TotalReqs
			account.Stats.CacheHits += stats.CacheHits
			if account.Stats.TotalReqs > 0 {
				account.Stats.CacheHitRate = float64(account.Stats.CacheHits) / float64(account.Stats.TotalReqs)
			}
		}
	}

	names := make([]string, 0, len(accounts))
	for name := range accounts {
		names = append(names, name)
	}
	sort.Strings(names)

	result := make([]codexAccountInfo, 0, len(names))
	for _, name := range names {
		result = append(result, *accounts[name])
	}
	return result
}

func (pm *ProxyManager) apiCodexUsage(c *gin.Context) {
	if pm.auditStore == nil {
		c.JSON(http.StatusOK, []interface{}{})
		return
	}
	period := c.DefaultQuery("period", "7d")
	entries, err := pm.auditStore.GetCodexUsage(period)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	if entries == nil {
		entries = []audit.CodexUsageEntry{}
	}
	c.JSON(http.StatusOK, entries)
}

func (pm *ProxyManager) apiCodexUserUsage(c *gin.Context) {
	if pm.auditStore == nil {
		c.JSON(http.StatusOK, []interface{}{})
		return
	}
	userID, err := strconv.ParseInt(c.Param("user_id"), 10, 64)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid user_id"})
		return
	}
	period := c.DefaultQuery("period", "7d")
	entries, err := pm.auditStore.GetCodexUserUsage(userID, period)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	if entries == nil {
		entries = []audit.CodexUserUsageEntry{}
	}
	c.JSON(http.StatusOK, entries)
}

func inferCaptureContentTypes(capture *ReqRespCapture) {
	if capture == nil {
		return
	}
	if getHeaderValue(capture.RespHeaders, "Content-Type") == "" {
		if contentType := inferBodyContentType(capture.RespBody); contentType != "" {
			if capture.RespHeaders == nil {
				capture.RespHeaders = make(map[string]string)
			}
			capture.RespHeaders["Content-Type"] = contentType
		}
	}
	if getHeaderValue(capture.ReqHeaders, "Content-Type") == "" {
		if contentType := inferBodyContentType(capture.ReqBody); contentType != "" {
			if capture.ReqHeaders == nil {
				capture.ReqHeaders = make(map[string]string)
			}
			capture.ReqHeaders["Content-Type"] = contentType
		}
	}
}

func getHeaderValue(headers map[string]string, name string) string {
	for key, value := range headers {
		if strings.EqualFold(key, name) {
			return value
		}
	}
	return ""
}

func inferBodyContentType(body []byte) string {
	trimmed := bytes.TrimSpace(body)
	if len(trimmed) == 0 {
		return ""
	}
	if bytes.HasPrefix(trimmed, []byte("event:")) || bytes.HasPrefix(trimmed, []byte("data:")) {
		return "text/event-stream; charset=utf-8"
	}
	if json.Valid(trimmed) {
		return "application/json"
	}
	if http.DetectContentType(trimmed) == "text/plain; charset=utf-8" {
		return "text/plain; charset=utf-8"
	}
	return ""
}

func (pm *ProxyManager) apiAuditUsers(c *gin.Context) {
	users, err := pm.auditStore.ListUsers()
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to list audit users"})
		return
	}
	c.JSON(http.StatusOK, users)
}

func (pm *ProxyManager) apiAuditActivity(c *gin.Context) {
	limit := 200
	if l, err := strconv.Atoi(c.DefaultQuery("limit", "200")); err == nil && l > 0 && l <= 1000 {
		limit = l
	}
	entries, err := pm.auditStore.GetRecentActivity(limit)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to get activity"})
		return
	}
	if entries == nil {
		entries = []audit.ActivityEntry{}
	}
	c.JSON(http.StatusOK, entries)
}

func (pm *ProxyManager) apiAuditUsage(c *gin.Context) {
	period := c.DefaultQuery("period", "24h")
	if period != "24h" && period != "7d" && period != "30d" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "period must be one of: 24h, 7d, 30d"})
		return
	}
	entries, err := pm.auditStore.GetUsageOverview(period)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to get usage overview"})
		return
	}
	c.JSON(http.StatusOK, entries)
}

func (pm *ProxyManager) apiAuditUserUsage(c *gin.Context) {
	userIDStr := c.Param("user_id")
	userID, err := strconv.ParseInt(userIDStr, 10, 64)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid user_id"})
		return
	}
	period := c.DefaultQuery("period", "24h")
	if period != "24h" && period != "7d" && period != "30d" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "period must be one of: 24h, 7d, 30d"})
		return
	}
	entries, err := pm.auditStore.GetUserUsage(userID, period)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to get user usage"})
		return
	}
	c.JSON(http.StatusOK, entries)
}

func (pm *ProxyManager) apiAuditCaptures(c *gin.Context) {
	userIDStr := c.Query("user_id")
	if userIDStr == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "user_id query parameter required"})
		return
	}
	userID, err := strconv.ParseInt(userIDStr, 10, 64)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid user_id"})
		return
	}
	limit := 50
	if l, err := strconv.Atoi(c.DefaultQuery("limit", "50")); err == nil && l > 0 {
		limit = l
	}
	offset := 0
	if o, err := strconv.Atoi(c.DefaultQuery("offset", "0")); err == nil && o >= 0 {
		offset = o
	}
	captures, err := pm.auditStore.ListCaptures(userID, limit, offset)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to list captures"})
		return
	}
	c.JSON(http.StatusOK, captures)
}

func (pm *ProxyManager) wireCodexQuotaPersistence(auditStore *audit.AuditStore) {
	if pm.peerProxy == nil {
		return
	}
	codexProxies := pm.peerProxy.GetCodexProxies()

	for _, proxy := range codexProxies {
		if proxy == nil {
			continue
		}
		proxy.SetOnQuotaPersist(func(account string, snapshot codex.QuotaSnapshot) {
			data, err := json.Marshal(snapshot)
			if err != nil {
				return
			}
			if err := auditStore.SaveCodexQuotaSnapshot(account, data); err != nil {
				pm.proxyLogger.Warnf("failed to persist codex quota snapshot for %q: %v", account, err)
			}
		})
	}

	rows, err := auditStore.LoadCodexQuotaSnapshots()
	if err != nil {
		pm.proxyLogger.Warnf("failed to load codex quota snapshots: %v", err)
		return
	}
	for _, row := range rows {
		var snapshot codex.QuotaSnapshot
		if err := json.Unmarshal([]byte(row.Snapshot), &snapshot); err != nil {
			continue
		}
		for _, proxy := range codexProxies {
			if proxy != nil {
				proxy.RestoreQuotaSnapshots(map[string]codex.QuotaSnapshot{row.AccountName: snapshot})
			}
		}
	}
	if len(rows) > 0 {
		pm.proxyLogger.Infof("restored %d codex quota snapshots from database", len(rows))
	}
}
