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

## 高频绕过陷阱（禁止清单）

以下四种写法是已知的安全漏洞模式，代码审查时必须驳回：

**陷阱 1：PolicyGate nil 旁路（R1.14）**
```go
// 禁止：nil 时静默跳过安全门
if s.installMgr != nil {
    s.installMgr.InstallExtension(...) // 安全检查
}
// 无论如何继续安装 ← 漏洞
```
正确做法：nil → `http.Error(503)` + `return`。安全门是强制路径。

**陷阱 2：MCP stdio 子进程继承完整父环境（R1.15）**
```go
// 禁止：将宿主密钥类环境变量传入 MCP 子进程
cmd.Env = os.Environ()
// 或者不设置 cmd.Env（Go exec 默认同样继承父进程环境）
```
正确做法：`cmd.Env = sanitizeParentEnv()` 过滤 `*_KEY/_TOKEN/_SECRET` 等，再叠加 `MCPClientConfig.Env`。

**陷阱 3：Bundle 子 MCP 跳过独立门控**
```go
// 禁止：父插件通过了安全门，子 MCP 直接写库
for name, def := range bundle.MCPServers {
    s.installBundleMCP(ctx, ...) // 无 PolicyGate 调用
}
```
正确做法：`installBundleMCP` 内部独立调用 `installMgr.InstallExtension`，失败则 skip+Warn，不中断父插件。

**陷阱 4：并行安装路径跳过 PolicyGate**
```go
// 禁止：POST /v1/mcp-servers 直接写库，不调用 Manager.InstallExtension
s.db.ExecContext(ctx, "INSERT INTO mcp_servers ...")
go s.startMCPServer(c)
```
正确做法：所有写入 `mcp_servers` / `extension_instances` 的端点都必须先调用 `Manager.InstallExtension`。见 M13-bis §6.1。
