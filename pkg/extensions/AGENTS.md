# 模块: Extensions Layer (L2)

> 扩展系统层负责管理外部能力（MCP Servers, Skills, Plugins）的市场分发、同步与安装集成。
> 架构参考: `docs/arch/M13-bis-Extension-Registry.md`

## 职责边界 (Responsibilities)

- **[是] 插件市场源管理**: 从云端、本地拉取市场列表（`marketplaces.yaml`）。
- **[是] 统一扩展安装与索引**: 将外部 bundle 安装并入库至 `extension_instances` 作为 SSoT。
- **[是] 原生集成工具**: 提供像 `computer_use` (OS 控制)、`browser_use` (无头浏览器) 等 L2 层面上复杂依赖的封装。
- **[不是] 执行 OS 底层命令**: 禁止调用 `os/exec` 或无沙箱的 `os.ReadFile`。任何直接触碰 OS 文件系统与进程的能力均应存在于 `pkg/action/tool` (生存工具集)。
- **[不是] 策略网关**: 信任策略 (TrustTier) 的判定由 `pkg/governance/policy` 决定，扩展层仅负责原样传递 `trust_tier`。

## 目录结构
- `marketplace/` - 插件市场客户端（获取 GitHub 等外部源）。
- `native/` - 原生的高级集成（Computer Use, Browser Use）。这些提供复杂环境交互能力。
- `mcp/` - 连接 MCP (Model Context Protocol) 进程/服务器的基础设施桥梁。
- `skill/` - 管理 Skill 规范解析与 Wasm 沙箱委托。

## 编码规则
- L2 模块不能直接反向引用 L3 (pkg/edge) 或 L4 模块。
- 此模块内的工具不应拥有裸 `http.Client` 逃逸，所有外发网络强制使用 M11 `SafeDialer` [XR-06]。
- 此模块内的逻辑需要执行 `bash` 或文件读写时，应调度对应的内置沙箱工具执行，或者由 `pkg/action/sandbox` 提供安全接口 [XR-11]。
