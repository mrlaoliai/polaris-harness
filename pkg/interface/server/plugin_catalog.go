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

// RegistryEntry 插件目录条目（ADR-0016：增加 Publisher/TrustTier/Type 字段）。
type RegistryEntry struct {
	// ID 全局唯一 slug，格式："{publisher}/{name}" 或 "mcp/{name}"
	ID string `json:"id" yaml:"id"`
	// Publisher 来源组织
	Publisher string `json:"publisher" yaml:"publisher"`
	// Type 条目类型："mcp" | "skill" | "plugin"（bundle）
	Type string `json:"type" yaml:"type"`
	// TrustTier 信任级别（ADR-0016 §2.1）。
	TrustTier int `json:"trust_tier" yaml:"trust_tier"`

	Name        string            `json:"name" yaml:"name"`
	Description string            `json:"description" yaml:"description"`
	Transport   string            `json:"transport,omitempty" yaml:"transport,omitempty"` // "stdio" | "sse" | "streamable_http"
	Command     string            `json:"command,omitempty" yaml:"command,omitempty"`     // stdio 命令
	Args        []string          `json:"args,omitempty" yaml:"args,omitempty"`           // 默认参数
	Env         map[string]string `json:"env,omitempty" yaml:"env,omitempty"`
	URL         string            `json:"url,omitempty" yaml:"url,omitempty"` // SSE / Streamable HTTP 端点
	Tags        []string          `json:"tags" yaml:"tags"`
	Homepage    string            `json:"homepage,omitempty" yaml:"homepage,omitempty"`
	Timeout     int               `json:"timeout" yaml:"timeout"` // 推荐超时秒数
	// 运行时叠加：是否已安装（mcp_servers 表中存在同 catalog_id）
	Installed bool `json:"installed" yaml:"installed"`
}

// builtinRegistry 内置推荐插件目录。
// 来源：MCP 官方 servers（TrustOfficial=3）+ 三大平台官方工具（TrustOfficial=3）。
// TrustTier 由 Polaris 白名单决定（ADR-0016 §2.1），非 author 自定义。

// handleListPluginCatalog 返回内置推荐插件目录列表，并叠加已安装状态。
// GET /v1/plugins/catalog
func (s *Server) getInstalledCatalogIDs(ctx context.Context) map[string]bool {
	installed := map[string]bool{}
	queries := []string{
		`SELECT catalog_id FROM mcp_servers WHERE catalog_id != '' AND catalog_id IS NOT NULL`,
		`SELECT catalog_id FROM skill_sources WHERE catalog_id != '' AND catalog_id IS NOT NULL`,
		`SELECT catalog_id FROM skills WHERE catalog_id != '' AND catalog_id IS NOT NULL`,
		`SELECT catalog_id FROM plugins WHERE catalog_id != '' AND catalog_id IS NOT NULL`,
		`SELECT catalog_id FROM apps WHERE catalog_id != '' AND catalog_id IS NOT NULL`,
	}
	for _, query := range queries {
		rows, err := s.db.QueryContext(ctx, query)
		if err != nil {
			continue
		}
		for rows.Next() {
			var cid string
			if rows.Scan(&cid) == nil {
				installed[cid] = true
			}
		}
		rows.Close()
	}
	return installed
}

func (s *Server) appendCustomCatalogs(ctx context.Context, result []RegistryEntry, installed map[string]bool) []RegistryEntry {
	// Fetch Custom MCP
	if rows, err := s.db.QueryContext(ctx, `SELECT id, name, transport, command, url FROM mcp_servers WHERE catalog_id = ''`); err == nil {
		for rows.Next() {
			var m RegistryEntry
			m.Type, m.Publisher, m.Installed = "mcp", "user", true
			if err := rows.Scan(&m.ID, &m.Name, &m.Transport, &m.Command, &m.URL); err == nil {
				result = append(result, m)
			}
		}
		rows.Close()
	}

	// Fetch Custom Skills
	if rows2, err := s.db.QueryContext(ctx, `SELECT id, name, description, repo_url FROM skills`); err == nil {
		for rows2.Next() {
			var m RegistryEntry
			m.Type, m.Publisher, m.Installed = "skill", "user", true
			if err := rows2.Scan(&m.ID, &m.Name, &m.Description, &m.URL); err == nil {
				result = append(result, m)
			}
		}
		rows2.Close()
	}

	// Fetch Custom Plugins
	if rows3, err := s.db.QueryContext(ctx, `SELECT id, name, description, manifest_url FROM plugins`); err == nil {
		for rows3.Next() {
			var m RegistryEntry
			m.Type, m.Publisher, m.Installed = "plugin", "user", true
			if err := rows3.Scan(&m.ID, &m.Name, &m.Description, &m.URL); err == nil {
				result = append(result, m)
			}
		}
		rows3.Close()
	}

	// Fetch Custom Apps
	if rows4, err := s.db.QueryContext(ctx, `SELECT id, name, description, url FROM apps`); err == nil {
		for rows4.Next() {
			var m RegistryEntry
			m.Type, m.Publisher, m.Installed = "app", "user", true
			if err := rows4.Scan(&m.ID, &m.Name, &m.Description, &m.URL); err == nil {
				result = append(result, m)
			}
		}
		rows4.Close()
	}
	return result
}

func (s *Server) appendCachedCatalogs(ctx context.Context, result []RegistryEntry, installed map[string]bool) []RegistryEntry {
	// Fetch Marketplaces Cached Catalog
	if rows5, err := s.db.QueryContext(ctx, `SELECT payload FROM registry_cache`); err == nil {
		for rows5.Next() {
			var payload string
			if err := rows5.Scan(&payload); err == nil {
				var entry RegistryEntry
				if err := json.Unmarshal([]byte(payload), &entry); err == nil {
					entry.Installed = installed[entry.ID]
					result = append(result, entry)
				}
			}
		}
		rows5.Close()
	}
	return result
}

// handleListPluginCatalog 返回内置推荐插件目录列表，并叠加已安装状态。
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
	// 可选覆盖：若不传则使用 catalog 默认值
	Name    string            `json:"name,omitempty"`
	Args    []string          `json:"args,omitempty"`
	Env     map[string]string `json:"env,omitempty"`
	URL     string            `json:"url,omitempty"`
	Timeout int               `json:"timeout,omitempty"`
}

// handleInstallPlugin 一键安装推荐插件。
// Type=mcp → 写入 mcp_servers 并异步连接；Type=skill/plugin → 写入 skill_sources。
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

	// 从数据库缓存查找 catalog 条目 (SSoT)
	var entry *RegistryEntry
	var payload string
	err := s.db.QueryRowContext(r.Context(), `SELECT payload FROM registry_cache WHERE id=?`, req.CatalogID).Scan(&payload)
	if err == nil {
		var e RegistryEntry
		if err := json.Unmarshal([]byte(payload), &e); err == nil {
			entry = &e
		}
	}
	if entry == nil {
		http.Error(w, "catalog entry not found: "+req.CatalogID, http.StatusNotFound)
		return
	}

	now := time.Now().UTC().Format(time.RFC3339)
	b := make([]byte, 8)
	_, _ = rand.Read(b)

	switch entry.Type {
	case "skill", "plugin":
		s.installSkillSource(w, r, entry, req, b, now)
	default: // "mcp" 及空值
		s.installMCPServer(w, r, entry, req, b, now)
	}
}

// installSkillSource 将 skill/plugin 类型 catalog 条目写入 skill_sources 表。
func (s *Server) installSkillSource(w http.ResponseWriter, r *http.Request,
	entry *RegistryEntry, req pluginInstallRequest, b []byte, now string) {

	// 防重复安装
	var existCount int
	s.db.QueryRowContext(r.Context(), //nolint:errcheck
		`SELECT COUNT(*) FROM skill_sources WHERE catalog_id=?`, req.CatalogID).Scan(&existCount) //nolint:errcheck
	if existCount > 0 {
		http.Error(w, "already installed", http.StatusConflict)
		return
	}

	srcID := "src_" + hex.EncodeToString(b)
	name := entry.Name
	if req.Name != "" {
		name = req.Name
	}
	repoURL := entry.URL
	if req.URL != "" {
		repoURL = req.URL
	}

	_, err := s.db.ExecContext(r.Context(),
		`INSERT INTO skill_sources(id, name, type, publisher, trust_tier, repo_url, catalog_id, enabled, created_at, updated_at)
         VALUES(?,?,?,?,?,?,?,1,?,?)`,
		srcID, name, entry.Type, entry.Publisher, entry.TrustTier, repoURL, req.CatalogID, now, now)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	json.NewEncoder(w).Encode(map[string]any{ //nolint:errcheck
		"id":         srcID,
		"type":       entry.Type,
		"name":       name,
		"publisher":  entry.Publisher,
		"trust_tier": entry.TrustTier,
		"repo_url":   repoURL,
		"catalog_id": req.CatalogID,
		"created_at": now,
	})
}

// installMCPServer 将 mcp 类型 catalog 条目写入 mcp_servers 表并异步连接。
func (s *Server) installMCPServer(w http.ResponseWriter, r *http.Request,
	entry *RegistryEntry, req pluginInstallRequest, b []byte, now string) {

	// 防重复安装
	var existCount int
	s.db.QueryRowContext(r.Context(), //nolint:errcheck
		`SELECT COUNT(*) FROM mcp_servers WHERE catalog_id=?`, req.CatalogID).Scan(&existCount) //nolint:errcheck
	if existCount > 0 {
		http.Error(w, "plugin already installed", http.StatusConflict)
		return
	}

	// 合并请求覆盖值（TrustTier 从 catalog 继承，不允许请求覆盖）
	c := MCPServerConfig{
		Transport: entry.Transport,
		Command:   entry.Command,
		Args:      entry.Args,
		Env:       entry.Env,
		URL:       entry.URL,
		Timeout:   entry.Timeout,
		TrustTier: entry.TrustTier,
		Enabled:   true,
	}
	if req.Name != "" {
		c.Name = req.Name
	} else {
		c.Name = entry.Name
	}
	if len(req.Args) > 0 {
		c.Args = req.Args
	}
	if len(req.Env) > 0 {
		merged := make(map[string]string, len(c.Env)+len(req.Env))
		maps.Copy(merged, c.Env)
		maps.Copy(merged, req.Env)
		c.Env = merged
	}
	if req.URL != "" {
		c.URL = req.URL
	}
	if req.Timeout > 0 {
		c.Timeout = req.Timeout
	}
	c.ID = "mcp_" + hex.EncodeToString(b)

	argsBytes, _ := json.Marshal(c.Args)
	envBytes, _ := json.Marshal(c.Env)

	_, err := s.db.ExecContext(r.Context(),
		`INSERT INTO mcp_servers(id, name, transport, command, args, env, url, enabled, timeout, trust_tier, catalog_id, created_at, updated_at)
         VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		c.ID, c.Name, c.Transport, c.Command, string(argsBytes), string(envBytes),
		c.URL, 1, c.Timeout, c.TrustTier, req.CatalogID, now, now)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	if s.mcpMgr != nil {
		go s.startMCPServer(c)
	}

	c.CreatedAt, c.UpdatedAt = now, now
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	json.NewEncoder(w).Encode(map[string]any{ //nolint:errcheck
		"server":     c,
		"catalog_id": req.CatalogID,
	})
}

// handleUninstallPlugin 卸载插件（通过 catalog_id 定位，自动识别 mcp_servers / skill_sources）。
// DELETE /v1/plugins/{catalogID}
func (s *Server) handleUninstallPlugin(w http.ResponseWriter, r *http.Request) {
	catalogID := r.PathValue("catalogID")
	removed := false

	// 尝试从 mcp_servers 删除（Type=mcp）
	rows, err := s.db.QueryContext(r.Context(),
		`SELECT id FROM mcp_servers WHERE catalog_id=?`, catalogID)
	if err == nil {
		var ids []string
		for rows.Next() {
			var id string
			if rows.Scan(&id) == nil {
				ids = append(ids, id)
			}
		}
		rows.Close()
		for _, id := range ids {
			if s.mcpMgr != nil {
				s.mcpMgr.Remove(id)
			}
			s.db.ExecContext(r.Context(), `DELETE FROM mcp_servers WHERE id=?`, id) //nolint:errcheck
			removed = true
		}
	}

	// 尝试从 skill_sources 删除（Type=skill/plugin）
	res, err := s.db.ExecContext(r.Context(),
		`DELETE FROM skill_sources WHERE catalog_id=?`, catalogID)
	if err == nil {
		if n, _ := res.RowsAffected(); n > 0 {
			removed = true
		}
	}

	if !removed {
		http.Error(w, "plugin not installed", http.StatusNotFound)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{"status": "uninstalled"}) //nolint:errcheck
}

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

// handleListMarketplaces 获取插件市场列表。
// GET /v1/plugins/marketplaces
func (s *Server) handleListMarketplaces(w http.ResponseWriter, r *http.Request) {
	var mps []Marketplace
	rows, err := s.db.QueryContext(r.Context(), "SELECT id, name, type, publisher, repo_url, description, is_builtin, trust_tier, enabled, created_at FROM plugin_marketplaces")
	if err == nil {
		for rows.Next() {
			var m Marketplace
			if err := rows.Scan(&m.ID, &m.Name, &m.Type, &m.Publisher, &m.RepoURL, &m.Description, &m.IsBuiltin, &m.TrustTier, &m.Enabled, &m.CreatedAt); err == nil {
				mps = append(mps, m)
			}
		}
		rows.Close()
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{"marketplaces": mps, "total": len(mps)})
}

// handleAddMarketplace 添加自定义市场
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
		"INSERT INTO plugin_marketplaces(id, name, type, publisher, repo_url, description, is_builtin, trust_tier, enabled, created_at) VALUES(?,?,?,?,?,?,?,?,?,?)",
		req.ID, req.Name, req.Type, req.Publisher, req.RepoURL, req.Description, req.IsBuiltin, req.TrustTier, req.Enabled, req.CreatedAt)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	_ = json.NewEncoder(w).Encode(req)
}

// handleDeleteMarketplace 删除市场
func (s *Server) handleDeleteMarketplace(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	// 只能删除非内置市场
	res, err := s.db.ExecContext(r.Context(), "DELETE FROM plugin_marketplaces WHERE id=? AND is_builtin=0", id)
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
