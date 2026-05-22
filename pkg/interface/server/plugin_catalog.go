package server

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"maps"
	"net/http"
	"time"
)

// RegistryEntry 插件目录条目（ADR-0016：Publisher/TrustTier/Type 字段）。
type RegistryEntry struct {
	// ID 全局唯一 slug，格式："{publisher}/{name}" 或 "mcp/{name}"
	ID        string `json:"id" yaml:"id"`
	Publisher string `json:"publisher" yaml:"publisher"`
	// Type "mcp" | "skill" | "plugin" | "app"
	Type      string `json:"type" yaml:"type"`
	TrustTier int    `json:"trust_tier" yaml:"trust_tier"`

	Name        string            `json:"name" yaml:"name"`
	Description string            `json:"description" yaml:"description"`
	Transport   string            `json:"transport,omitempty" yaml:"transport,omitempty"`
	Command     string            `json:"command,omitempty" yaml:"command,omitempty"`
	Args        []string          `json:"args,omitempty" yaml:"args,omitempty"`
	Env         map[string]string `json:"env,omitempty" yaml:"env,omitempty"`
	URL         string            `json:"url,omitempty" yaml:"url,omitempty"`
	Tags        []string          `json:"tags" yaml:"tags"`
	Homepage    string            `json:"homepage,omitempty" yaml:"homepage,omitempty"`
	Timeout     int               `json:"timeout" yaml:"timeout"`
	// 运行时叠加：是否已安装（extension_instances 表中存在同 catalog_id）
	Installed bool `json:"installed" yaml:"installed"`
}

// getInstalledCatalogIDs 返回所有已安装的 catalog_id 集合。
// SSoT：仅查 extension_instances，不再 UNION 多表。
func (s *Server) getInstalledCatalogIDs(ctx context.Context) map[string]bool {
	installed := map[string]bool{}
	rows, err := s.db.QueryContext(ctx,
		`SELECT catalog_id FROM extension_instances WHERE catalog_id != ''`)
	if err != nil {
		return installed
	}
	defer rows.Close()
	for rows.Next() {
		var cid string
		if rows.Scan(&cid) == nil {
			installed[cid] = true
		}
	}
	return installed
}

// appendCustomCatalogs 追加用户自建扩展（origin=user）到目录列表。
// 全走 extension_instances，不再散查 skills/plugins/apps 三表。
func (s *Server) appendCustomCatalogs(ctx context.Context, result []RegistryEntry, _ map[string]bool) []RegistryEntry {
	rows, err := s.db.QueryContext(ctx,
		`SELECT id, ext_type, name, publisher, trust_tier, config
		 FROM extension_instances
		 WHERE origin = 'user' AND enabled = 1 AND status = 'installed'`)
	if err != nil {
		return result
	}
	defer rows.Close()

	for rows.Next() {
		var e RegistryEntry
		var configJSON string
		if err := rows.Scan(&e.ID, &e.Type, &e.Name, &e.Publisher, &e.TrustTier, &configJSON); err != nil {
			continue
		}
		e.Installed = true
		// 从 config JSON 提取 URL（app）/ command（mcp）等展示字段，容错忽略
		var cfg map[string]any
		if json.Unmarshal([]byte(configJSON), &cfg) == nil {
			if v, ok := cfg["url"].(string); ok {
				e.URL = v
			}
			if v, ok := cfg["command"].(string); ok {
				e.Command = v
			}
		}
		result = append(result, e)
	}
	return result
}

// appendCachedCatalogs 追加市场同步缓存条目，叠加安装状态。
func (s *Server) appendCachedCatalogs(ctx context.Context, result []RegistryEntry, installed map[string]bool) []RegistryEntry {
	rows, err := s.db.QueryContext(ctx, `SELECT payload FROM registry_cache`)
	if err != nil {
		return result
	}
	defer rows.Close()

	for rows.Next() {
		var payload string
		if err := rows.Scan(&payload); err != nil {
			continue
		}
		var entry RegistryEntry
		if err := json.Unmarshal([]byte(payload), &entry); err != nil {
			continue
		}
		entry.Installed = installed[entry.ID]
		result = append(result, entry)
	}
	return result
}

// handleListPluginCatalog 返回扩展目录列表（用户自建 + 市场缓存）。
// GET /v1/plugins/catalog
func (s *Server) handleListPluginCatalog(w http.ResponseWriter, r *http.Request) {
	installed := s.getInstalledCatalogIDs(r.Context())
	result := make([]RegistryEntry, 0)
	result = s.appendCustomCatalogs(r.Context(), result, installed)
	result = s.appendCachedCatalogs(r.Context(), result, installed)

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{ //nolint:errcheck
		"catalog": result,
		"total":   len(result),
	})
}

// pluginInstallRequest 一键安装请求体。
type pluginInstallRequest struct {
	CatalogID string `json:"catalog_id"`
	// 可选覆盖：不传则使用 catalog 默认值
	Name    string            `json:"name,omitempty"`
	Args    []string          `json:"args,omitempty"`
	Env     map[string]string `json:"env,omitempty"`
	URL     string            `json:"url,omitempty"`
	Timeout int               `json:"timeout,omitempty"`
}

// handleInstallPlugin 一键安装目录条目。
// MCP → mcp_servers + extension_instances；Skill/Plugin → extension_instances（异步下载）。
// POST /v1/plugins/install
func (s *Server) handleInstallPlugin(w http.ResponseWriter, r *http.Request) {
	var req pluginInstallRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if req.CatalogID == "" {
		http.Error(w, "catalog_id is required", http.StatusBadRequest)
		return
	}

	// TrustUntrusted(0) 安装直接拒绝
	var trustTier int
	var payload string
	err := s.db.QueryRowContext(r.Context(),
		`SELECT trust_tier, payload FROM registry_cache WHERE id=?`, req.CatalogID).
		Scan(&trustTier, &payload)
	if err != nil {
		http.Error(w, "catalog entry not found: "+req.CatalogID, http.StatusNotFound)
		return
	}
	if trustTier == 0 {
		http.Error(w, "untrusted entry, installation rejected", http.StatusForbidden)
		return
	}

	var entry RegistryEntry
	if err := json.Unmarshal([]byte(payload), &entry); err != nil {
		http.Error(w, "malformed catalog entry", http.StatusInternalServerError)
		return
	}

	// 防重复
	var existCount int
	s.db.QueryRowContext(r.Context(), //nolint:errcheck
		`SELECT COUNT(*) FROM extension_instances WHERE catalog_id=?`, req.CatalogID).
		Scan(&existCount) //nolint:errcheck
	if existCount > 0 {
		http.Error(w, "already installed", http.StatusConflict)
		return
	}

	now := time.Now().UTC().Format(time.RFC3339)
	b := make([]byte, 8)
	_, _ = rand.Read(b)
	extID := "ext_" + hex.EncodeToString(b)

	switch entry.Type {
	case "mcp", "":
		s.installMCPExtension(w, r, extID, &entry, req, now)
	default: // skill | plugin | app
		s.installGenericExtension(w, r, extID, &entry, req, now)
	}
}

// installMCPExtension 安装 MCP 类型：写 extension_instances + mcp_servers + 异步启动。
func (s *Server) installMCPExtension(w http.ResponseWriter, r *http.Request,
	extID string, entry *RegistryEntry, req pluginInstallRequest, now string) {

	// 合并请求覆盖（trust_tier 不允许客户端覆盖）
	cfg := MCPServerConfig{
		Transport: entry.Transport,
		Command:   entry.Command,
		Args:      entry.Args,
		Env:       entry.Env,
		URL:       entry.URL,
		Timeout:   entry.Timeout,
		TrustTier: entry.TrustTier,
		Enabled:   true,
	}
	cfg.Name = cond(req.Name != "", req.Name, entry.Name)
	if len(req.Args) > 0 {
		cfg.Args = req.Args
	}
	if len(req.Env) > 0 {
		merged := make(map[string]string, len(cfg.Env)+len(req.Env))
		maps.Copy(merged, cfg.Env)
		maps.Copy(merged, req.Env)
		cfg.Env = merged
	}
	if req.URL != "" {
		cfg.URL = req.URL
	}
	if req.Timeout > 0 {
		cfg.Timeout = req.Timeout
	}

	mcpID := "mcp_" + extID[4:] // 共用随机字节
	cfg.ID = mcpID

	argsBytes, _ := json.Marshal(cfg.Args)
	envBytes, _ := json.Marshal(cfg.Env)

	// 写 extension_instances（先写，status=installed，runtime_id 关联 mcp_servers.id）
	_, err := s.db.ExecContext(r.Context(),
		`INSERT INTO extension_instances
		 (id, ext_type, origin, catalog_id, name, publisher, trust_tier, enabled,
		  runtime_id, install_path, status, created_at, updated_at)
		 VALUES(?,?,?,?,?,?,?,1,?,?,'installed',?,?)`,
		extID, "mcp", "marketplace", req.CatalogID,
		cfg.Name, entry.Publisher, entry.TrustTier,
		mcpID, "", now, now)
	if err != nil {
		http.Error(w, "extension_instances insert: "+err.Error(), http.StatusInternalServerError)
		return
	}

	// 写 mcp_servers
	_, err = s.db.ExecContext(r.Context(),
		`INSERT INTO mcp_servers(id, name, transport, command, args, env, url, enabled, timeout, trust_tier, catalog_id, created_at, updated_at)
		 VALUES(?,?,?,?,?,?,?,1,?,?,?,?,?)`,
		mcpID, cfg.Name, cfg.Transport, cfg.Command,
		string(argsBytes), string(envBytes),
		cfg.URL, cfg.Timeout, cfg.TrustTier, req.CatalogID, now, now)
	if err != nil {
		// 回滚 extension_instances
		s.db.ExecContext(r.Context(), `DELETE FROM extension_instances WHERE id=?`, extID) //nolint:errcheck
		http.Error(w, "mcp_servers insert: "+err.Error(), http.StatusInternalServerError)
		return
	}

	if s.mcpMgr != nil {
		go s.startMCPServer(cfg)
	}

	cfg.CreatedAt, cfg.UpdatedAt = now, now
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	json.NewEncoder(w).Encode(map[string]any{ //nolint:errcheck
		"id":         extID,
		"type":       "mcp",
		"server":     cfg,
		"catalog_id": req.CatalogID,
	})
}

// installGenericExtension 安装 skill / plugin / app：写 extension_instances。
// skill/plugin 需异步下载文件并写运行时表（TODO: downloadAndInstall goroutine）。
func (s *Server) installGenericExtension(w http.ResponseWriter, r *http.Request,
	extID string, entry *RegistryEntry, req pluginInstallRequest, now string) {

	name := cond(req.Name != "", req.Name, entry.Name)
	url := cond(req.URL != "", req.URL, entry.URL)

	configJSON, _ := json.Marshal(map[string]any{
		"url":        url,
		"repo_url":   url,
		"entrypoint": "",
	})

	status := "installed"
	if entry.Type == "skill" || entry.Type == "plugin" {
		// 异步下载阶段标记（实际 downloadAndInstall goroutine 待实现）
		status = "downloading"
	}

	_, err := s.db.ExecContext(r.Context(),
		`INSERT INTO extension_instances
		 (id, ext_type, origin, catalog_id, name, publisher, trust_tier, enabled,
		  runtime_id, install_path, config, status, created_at, updated_at)
		 VALUES(?,?,?,?,?,?,?,1,'','',?,?,?,?)`,
		extID, entry.Type, "marketplace", req.CatalogID,
		name, entry.Publisher, entry.TrustTier,
		string(configJSON), status, now, now)
	if err != nil {
		http.Error(w, "extension_instances insert: "+err.Error(), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	json.NewEncoder(w).Encode(map[string]any{ //nolint:errcheck
		"id":         extID,
		"type":       entry.Type,
		"name":       name,
		"publisher":  entry.Publisher,
		"trust_tier": entry.TrustTier,
		"catalog_id": req.CatalogID,
		"status":     status,
		"created_at": now,
	})
}

// handleUninstallPlugin 卸载扩展（通过 catalog_id 定位 extension_instances）。
// DELETE /v1/plugins/{catalogID}
func (s *Server) handleUninstallPlugin(w http.ResponseWriter, r *http.Request) {
	catalogID := r.PathValue("catalogID")

	// 查 extension_instances（SSoT）
	rows, err := s.db.QueryContext(r.Context(),
		`SELECT id, ext_type, runtime_id, install_path FROM extension_instances WHERE catalog_id=?`,
		catalogID)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	type instRow struct {
		id, extType, runtimeID, installPath string
	}
	var insts []instRow
	for rows.Next() {
		var inst instRow
		if rows.Scan(&inst.id, &inst.extType, &inst.runtimeID, &inst.installPath) == nil {
			insts = append(insts, inst)
		}
	}
	rows.Close()

	if len(insts) == 0 {
		http.Error(w, "extension not installed", http.StatusNotFound)
		return
	}

	for _, inst := range insts {
		switch inst.extType {
		case "mcp":
			if s.mcpMgr != nil && inst.runtimeID != "" {
				s.mcpMgr.Remove(inst.runtimeID)
			}
			s.db.ExecContext(r.Context(), `DELETE FROM mcp_servers WHERE id=?`, inst.runtimeID) //nolint:errcheck
		case "skill":
			if inst.runtimeID != "" {
				// Skill 废弃而非删除（历史记录保留）
				s.db.ExecContext(r.Context(), //nolint:errcheck
					`UPDATE skills SET deprecated=1, updated_at=CURRENT_TIMESTAMP WHERE name=?`,
					inst.runtimeID)
			}
		}
		// 删除 extension_instances 记录（子记录级联）
		s.db.ExecContext(r.Context(), //nolint:errcheck
			`DELETE FROM extension_instances WHERE id=? OR parent_id=?`, inst.id, inst.id)
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{"status": "uninstalled"}) //nolint:errcheck
}

// Marketplace CRUD ---------------------------------------------------------

// Marketplace 市场配置。
type Marketplace struct {
	ID          string `json:"id" yaml:"id"`
	Name        string `json:"name" yaml:"name"`
	Type        string `json:"type" yaml:"type"`
	Publisher   string `json:"publisher" yaml:"publisher"`
	RepoURL     string `json:"repo_url" yaml:"repo_url"`
	Description string `json:"description" yaml:"description"`
	IsBuiltin   int    `json:"is_builtin" yaml:"is_builtin"`
	TrustTier   int    `json:"trust_tier" yaml:"trust_tier"`
	Enabled     int    `json:"enabled" yaml:"enabled"`
	CreatedAt   string `json:"created_at" yaml:"created_at"`
}

// handleListMarketplaces GET /v1/plugins/marketplaces
func (s *Server) handleListMarketplaces(w http.ResponseWriter, r *http.Request) {
	var mps []Marketplace
	rows, err := s.db.QueryContext(r.Context(),
		`SELECT id, name, type, publisher, repo_url, description, is_builtin, trust_tier, enabled, created_at
		 FROM plugin_marketplaces`)
	if err == nil {
		for rows.Next() {
			var m Marketplace
			if rows.Scan(&m.ID, &m.Name, &m.Type, &m.Publisher, &m.RepoURL,
				&m.Description, &m.IsBuiltin, &m.TrustTier, &m.Enabled, &m.CreatedAt) == nil {
				mps = append(mps, m)
			}
		}
		rows.Close()
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{"marketplaces": mps, "total": len(mps)})
}

// handleAddMarketplace POST /v1/plugins/marketplaces
func (s *Server) handleAddMarketplace(w http.ResponseWriter, r *http.Request) {
	var req Marketplace
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	now := time.Now().UTC().Format(time.RFC3339)
	b := make([]byte, 4)
	_, _ = rand.Read(b)
	req.ID = "mp_" + hex.EncodeToString(b)
	req.IsBuiltin = 0
	req.TrustTier = 2 // Community
	req.Enabled = 1
	req.CreatedAt = now

	_, err := s.db.ExecContext(r.Context(),
		`INSERT INTO plugin_marketplaces
		 (id, name, type, publisher, repo_url, description, is_builtin, trust_tier, enabled, created_at)
		 VALUES(?,?,?,?,?,?,?,?,?,?)`,
		req.ID, req.Name, req.Type, req.Publisher, req.RepoURL,
		req.Description, req.IsBuiltin, req.TrustTier, req.Enabled, req.CreatedAt)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	_ = json.NewEncoder(w).Encode(req)
}

// handleDeleteMarketplace DELETE /v1/plugins/marketplaces/{id}
func (s *Server) handleDeleteMarketplace(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	res, err := s.db.ExecContext(r.Context(),
		`DELETE FROM plugin_marketplaces WHERE id=? AND is_builtin=0`, id)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if n, _ := res.RowsAffected(); n == 0 {
		http.Error(w, "marketplace not found or is builtin", http.StatusForbidden)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]string{"status": "deleted"})
}

// cond 三元运算辅助（Go 无三元）
func cond(pred bool, a, b string) string {
	if pred {
		return a
	}
	return b
}
