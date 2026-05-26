# 模块 13-bis: Extension Registry

> 扩展系统的市场、安装、运行时三层模型。覆盖 MCP / Skill / Plugin / App 全类型。[HE-Rule-3] [HE-Rule-6]
> **§跳读**: 0:职责边界 / 1:三层模型 / 2:类型定义 / 3:安装流 / 4:信任门控 / 5:文件系统 / 6:调用路由 / 7:学习技能归并 / 8:表引用速查

---

## 0. 职责边界

- **是**: 市场同步、目录展示、安装/卸载 API、安装状态追踪
- **是**: `extension_instances` 作为所有已安装扩展的单一事实来源（SSoT）
- **是**: 安装后运行时绑定（写 `mcp_servers` 或 `skills`）
- **不是**: MCP 进程生命周期管理（M7 MCPManager）
- **不是**: Wasm 执行与沙箱（M7 WazmRuntime）
- **不是**: Skill 检索与 Logic Collapse（M6）
- **不是**: 信任策略制定（M11 Cedar-Gate）

---

## 1. 三层模型

```
Layer 0  Market（目录层）
  plugin_marketplaces   市场来源注册；builtin=4条，用户可追加
  extension_catalog        市场同步快照；只读缓存，不驱动执行

Layer 1  Instances（安装层）← SSoT
  extension_instances   所有已安装扩展的统一记录
                        origin 区分来源，status 追踪异步安装进度

Layer 2  Runtime（运行时层）
  mcp_servers           MCP 进程连接配置；MCPManager 唯一消费方
  skills（008）         Wasm/Script 执行元数据；SkillExecutor 唯一消费方
```

**数据流方向**：`plugin_marketplaces → 同步 → extension_catalog → 安装 → extension_instances → 绑定 → mcp_servers / skills`

`extension_instances` 是唯一跨层视图。前端安装状态查询、卸载、刷新全走此表，不直接查运行时表。

---

## 2. 扩展类型

| ext_type | 运行时绑定 | 文件下载 | 典型来源 |
|----------|-----------|---------|---------|
| `mcp`    | `mcp_servers` | 是（或自管理进程） | marketplace / user |
| `skill`  | `skills`（008） | 是（script/wasm） | marketplace / learned |
| `plugin` | `plugins`（021）元数据 + 子组件各走 mcp_servers/skills | 是（bundle 解压） | marketplace |
| `app`    | 无（URL 直记，当前不支持运行时调用） | 否 | marketplace / user |

### 2.1 多厂商格式适配

市场插件包（`.tar.gz`）内的清单文件通过 `pkg/extensions/marketplace/adapter.go` 统一解析为 `RegistryEntry`：

| 清单文件 | 厂商 | 安装结果 |
|---------|------|---------|
| `ai-plugin.json`（api.type=mcp） | OpenAI | 写入 mcp_servers，启动 MCP 进程 |
| `ai-plugin.json`（api.type=openapi） | OpenAI | RegistryEntry type=app，URL 存储，**当前不可被 LLM 调用** |
| `.claude-plugin/plugin.toml` 或 `plugin.toml`（含 command） | Anthropic | 写入 mcp_servers |
| `.claude-plugin/plugin.json` | Anthropic | RegistryEntry type=plugin |
| `skills.yaml` / `agent-manifest.yaml`（含 command） | Google | 写入 mcp_servers |
| `skills.yaml`（无 command，含 name） | Google | 写入 skills（script runtime） |

Polaris 原生格式（`SKILL.md` / `plugin.json`）由 `pkg/extensions/marketplace/loader.go` 处理，不经此适配层。

**origin 枚举**：

| origin | 含义 | trust_tier 默认值 |
|--------|------|-----------------|
| `builtin`     | 仅限维持 Agent 基础生存的极简原生工具（如 `bash`, `search_extension`, `install_extension`） | 4 TrustSystem |
| `official`    | 官方云端市场下载的推荐扩展包（解耦二进制） | 3 TrustOfficial |
| `marketplace` | 第三方社区市场下载 | 继承 extension_catalog |
| `user`        | 用户手动创建/配置 | 1 TrustLocal |
| `learned`     | M9 自演化 promote | 1 TrustLocal |

---

## 3. 安装流

### 3.1 MCP

```
POST /v1/plugins/install {catalog_id, type=mcp}
  1. 写 extension_instances (status=installing)
  2. INSERT mcp_servers（继承 trust_tier）
  3. go MCPManager.startMCPServer()
  4. UPDATE extension_instances SET status=installed, runtime_id=mcp_servers.id
```

### 3.2 Skill

```
POST /v1/plugins/install {catalog_id, type=skill}
  1. 写 extension_instances (status=downloading)
  2. go downloadAndInstallSkill():
     a. HTTP 下载 tar.gz → 解压 → install_path
     b. 读取 SKILL.md → 解析 name/description frontmatter
     c. INSERT INTO skills(name, runtime='script', instructions=SKILL.md全文, ...)
     d. UPDATE extension_instances SET status=installed, runtime_id=runtimeID
```

**说明**：script runtime 技能 instructions 字段存储 SKILL.md 全文，供 LLM tool_use 时返回给 LLM 执行。wasm runtime 技能通过 Logic Collapse 编译（M6 §2.2），instructions 为空。

### 3.3 Plugin Bundle

```
POST /v1/plugins/install {catalog_id, type=plugin}
  1. 写 extension_instances (status=downloading, parent)
  2. go downloadAndInstallPlugin():
     a. HTTP 下载 tar.gz → 解压 → install_path
     b. 解析 plugin.json (PluginBundleManifest)：
        mcp_inline{}  → installBundleMCP() → mcp_servers + 子 extension_instances
        mcp_servers（.mcp.json 引用）→ 同上（路径安全校验 safeJoin）
        skills[]      → installBundleSkill() → skills + 子 extension_instances
        外部厂商格式  → adapter.ParseManifestDir() → 按类型分发
     c. INSERT plugins (021) 写入 bundle 元数据（entrypoint/env）
     d. UPDATE parent extension_instances SET status=installed
```

**注意**：`plugin.json` 中 `hooks` 字段当前仅解析，未执行写入 policies/ 目录（待 M11 Hook 框架接入）。子组件（MCP/Skill）通过 `parent_id` 关联到 extension_instances 父记录。

### 3.4 同步但不自动安装

系统启动时（`bootMarketplaceInit`），后台自动拉取所有 `is_builtin=1` 官方市场源至 `extension_catalog` 作为前端展示的缓存目录。**默认情况下不会静默强制安装**任何外部扩展（保持 Agent 基础生存极简）。用户需在前端主动点击安装，才触发对应运行时表的绑定及 `extension_instances` 的写入。

### 3.5 彻底卸载

```
DELETE /v1/plugins/{catalog_id}
  1. 查 extension_instances WHERE catalog_id=?
  2. 按 ext_type 清理运行时绑定（MCPManager.Remove / skills 表 deprecate / plugins 表记录）
  3. 通过底层管理接口安全删除 install_path 物理目录（严格禁止 HTTP Handler 裸写 os.RemoveAll）
  4. DELETE extension_instances（含子记录）
  5. [对于非 is_builtin 或 origin='user' 的第三方扩展] 级联 DELETE extension_catalog，确保彻底从前端列表消失
```

---

## 4. 信任门控

> 策略制定见 M11 Cedar-Gate。本节只描述 Extension Registry 的触发点。

**核心约束**：所有扩展安装路径（包括手动创建、Agent 自治安装、AI 生成技能）必须强制通过 `Manager.InstallExtension` 中央网关，确保策略拦截（自动/HITL审批/拒绝）不可绕过。
安装时 `trust_tier` 从 `extension_catalog` 继承（自建内容定为 TrustLocal 1），写入 `extension_instances` 和运行时表（`mcp_servers.trust_tier` / `skills.trust_tier`）。
同时结合系统全局的 `permission_mode`（`default` / `auto_review` / `full_access`）及扩展是否有钩子(hooks)，由 Cedar 引擎（`InstallSecurityGate`）统一拦截并决断（自动放行 / HITL 审批 / 强制拒绝），并记入 AuditTrail 日志。

**禁止**：安装请求的 `trust_tier` 字段不允许客户端覆盖（server 端强制忽略）。

TrustTier 及 PermissionMode 共同影响：

| trust_tier | 安装时 | 运行时 |
|------------|-------|-------|
| 4 System   | 不走此流（程序内嵌） | 直接执行 |
| 3 Official | 自动确认 | Sbx-L2，TaintMedium |
| 2 Community | 自动确认 | Sbx-L1，TaintHigh |
| 1 Local    | 用户确认弹窗 | Sbx-L1，TaintHigh，每次提示 |
| 0 Untrusted | 拒绝安装 | — |

---

## 5. 文件系统布局

```
~/.polaris-harness/
├── extensions/
│   ├── skill/
│   │   ├── marketplace/{ext_id}/   # 市场安装的 Skill
│   │   │   ├── SKILL.md
│   │   │   ├── impl.wasm           # 或 main.py / main.sh
│   │   │   └── SIGNATURE
│   │   └── learned/{ext_id}/       # M9 自演化 promote 的 Skill
│   └── plugin/{ext_id}/            # Plugin Bundle 解压
│       ├── plugin.json
│       ├── skills/
│       └── hooks/
├── cache/{marketplace_id}/         # 市场同步临时下载区（安装完成后清理）
└── polaris.db
```

`extension_instances.install_path` 记录绝对路径。MCP 和 App 的 `install_path` 为空字符串。

---

## 6. 调用路由

```
推理时 buildToolSchemas() 构建工具列表（每次请求调用）：
  ├── toolReg.List()              → builtin 工具 schema（startup 注入）
  ├── mcpMgr.ListToolSchemas()   → 已连接 MCP 工具 schema
  └── SELECT skills WHERE runtime='script' AND deprecated=0
                                 → "skill:{name}" 工具 schema

LLM 发出 tool_use {name, input}
  → SetToolExecutor → toolExec closure
  ├── name 以 "skill:" 开头
  │     → 读 skills.instructions + 拼接 input → 返回给 LLM
  │       （LLM 按 SKILL.md 指令处理输入，产出结果）
  └── 其他名称 → sandboxRouter.Execute → InProcessSandbox.Execute(name)
         ├── builtin 工具（startup 由 RegisterBuiltinTools 注入） → 直接执行
         └── MCP 工具（MCPManager.Add 时由 registerTools 注入）
               → client.CallToolTainted() → 外部 MCP 进程
```

**内置工具**（`origin=builtin`）启动时注入 InProcessSandbox，不经市场流程。

**MCP 工具**：`MCPManager.LoadFromDB()` 启动时异步恢复已安装 MCP，`Add()` 时发现工具并注册到 InProcessSandbox。工具名格式：`{serverID}_{toolName}`。

**App 类型**：当前仅 URL 存储，无 AppRunner / WebView，LLM 不可调用（待后续实现）。

---

## 7. 学习技能归并（M9 → Extension Registry）

M9 Self-Improvement Engine 在 `L2SkillGeneration` 阶段 promote 候选技能时：

1. 写 `extension_instances`（`ext_type=skill, origin=learned, trust_tier=1`）
2. 调用 `SkillRegistry.Register()`，写 `skills`（008）表
3. `install_path` 指向 `extensions/skill/learned/{ext_id}/`

**禁止**：M9 直接写 `skills` 表（inv_M6_02）。必须经 `extension_instances` → `SkillRegistry` 路径。

前端"技能"Tab 通过 `origin=learned` 显示"AI 生成"标签，与"市场安装""内置"并列展示。

---

## 8. 表引用速查

| 表 | 迁移文件 | 消费方 |
|----|---------|-------|
| `plugin_marketplaces` | 018 | M13 API |
| `extension_catalog` | 019 | M13 API |
| `extension_instances` | 020 | M13 API（SSoT） |
| `mcp_servers` | 015 | M7 MCPManager |
| `skills` | 008 | M6 SkillRegistry |
| `plugins` | 021 | plugin_catalog.go（bundle 元数据写入，子组件各走 mcp_servers/skills） |

**已删除**（不再存在）：`skill_sources`、`apps`——职责统一归入 `extension_instances`（020）。
