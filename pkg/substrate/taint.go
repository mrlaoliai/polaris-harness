package substrate

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"sync"

	"github.com/polarisagi/polaris-harness/internal/protocol"
)

// Taint Tracking — 污点追踪类型系统。
// 权威 TaintLevel 枚举定义见 internal/protocol/types.go。
// 架构文档: docs/arch/11-Policy-Safety-深度选型.md §2

// TaintedString 是带污点标记的字符串。
// content 未导出——仅 Sanitize() 可构造 SafeString。
// PromptBuilder.WriteInstruction 仅接受 SafeString。
type TaintedString struct {
	content string
	Source  TaintSource
	Origin  string
}

// SafeString 是经清洗的安全字符串，仅 Sanitize() 可构造。
type SafeString struct {
	content string
}

// TaintSource 记录污点来源。
type TaintSource struct {
	Module           string
	EntityID         string
	EventID          string
	OriginTaintLevel protocol.TaintLevel
}

// NewTaintedString 创建一个带有安全污点标记的字符串。
// 默认情况下，只要被包裹，外部就无法轻易将它作为普通字符串拼接到 Prompt 中。
func NewTaintedString(content string, source TaintSource, origin string) TaintedString {
	return TaintedString{
		content: content,
		Source:  source,
		Origin:  origin,
	}
}

// Content 获取受污染的原始内容。
// 注意：只应在明确不需要安全清洗的场景下使用此方法（如：写入数据库、发送到受限沙箱的数据槽）。
func (ts TaintedString) Content() string {
	return ts.content
}

// Level 获取当前的污点等级。
func (ts TaintedString) Level() protocol.TaintLevel {
	return ts.Source.OriginTaintLevel
}

// Content 获取已清洗的绝对安全字符串。
// 此字符串可以安全地注入到 LLM 的 Instruction Slot 中。
func (ss SafeString) Content() string {
	return ss.content
}

// =============================================================================
// TaintTracker — 运行时污点传播追踪器（M11 §2.1 第一重防护）
// 追踪每个字符串 ID 的污点等级，GetMaxTaint 实现 PropagateTaint 语义：
// output = max(所有输入的 TaintLevel)，只升不降。
// =============================================================================

// TaintTracker 线程安全的运行时污点追踪器。
type TaintTracker struct {
	mu     sync.RWMutex
	levels map[string]protocol.TaintLevel // id → TaintLevel
}

// NewTaintTracker 创建新的追踪器实例。
func NewTaintTracker() *TaintTracker {
	return &TaintTracker{
		levels: make(map[string]protocol.TaintLevel),
	}
}

// Track 记录字符串 ID 的污点等级。
// 遵循单调不递减原则：只能升级，不能降级。
func (tt *TaintTracker) Track(id string, level protocol.TaintLevel) {
	tt.mu.Lock()
	defer tt.mu.Unlock()
	if existing, ok := tt.levels[id]; !ok || level > existing {
		tt.levels[id] = level
	}
}

// GetLevel 获取单个 ID 的当前污点等级。
func (tt *TaintTracker) GetLevel(id string) protocol.TaintLevel {
	tt.mu.RLock()
	defer tt.mu.RUnlock()
	return tt.levels[id]
}

// GetMaxTaint 实现 PropagateTaint 语义：返回所有指定 ID 中最高的污点等级。
// 用于合并多个输入的污点，决定输出的最终污点等级。
func (tt *TaintTracker) GetMaxTaint(ids ...string) protocol.TaintLevel {
	tt.mu.RLock()
	defer tt.mu.RUnlock()
	var max protocol.TaintLevel
	for _, id := range ids {
		if l, ok := tt.levels[id]; ok && l > max {
			max = l
		}
	}
	return max
}

// =============================================================================
// Spotlighting — 不可信数据围栏标记（M11 §2.2）
// 步骤1: 生成标记 = SHA-256(content)[:8]（内容派生，保证重放确定性）
// 步骤2: 包裹为 "=== UNTRUSTED_DATA_{hex} ===\n{data}\n=== END_UNTRUSTED_DATA ==="
// 调用方: M5 ContextAssembler.Build（ZoneTaintedData 追加前强制包裹）
// =============================================================================

// Spotlighting 对不可信数据槽内容加围栏标记，防止 LLM 将其解析为系统指令。
// 仅适用于 TaintMedium 及以上的内容；TaintLow/TaintNone 无需包裹。
func Spotlighting(ts TaintedString) string {
	if ts.Source.OriginTaintLevel < protocol.TaintMedium {
		return ts.content
	}
	hash := sha256.Sum256([]byte(ts.content))
	marker := hex.EncodeToString(hash[:])[:8]
	return fmt.Sprintf("=== UNTRUSTED_DATA_%s ===\n%s\n=== END_UNTRUSTED_DATA ===", marker, ts.content)
}

// =============================================================================
// TaintBoundary — 跨模块边界 HMAC 验证（inv_M11_02）
// 防止反序列化路径绕过污点标记：序列化时附加 HMAC-SHA256，
// 反序列化时重新计算并对比，不匹配则强制升级到 TaintHigh。
// key 由调用方从 Capability Token 派生（或使用共享密钥），不存储于负载中。
// =============================================================================

// TaintBoundarySerializer 跨边界污点序列化器。
type TaintBoundarySerializer struct {
	key []byte // HMAC-SHA256 密钥（由调用方从 CapToken 派生）
}

// NewTaintBoundarySerializer 创建序列化器。key 不得为空（fail-fast）。
func NewTaintBoundarySerializer(key []byte) *TaintBoundarySerializer {
	if len(key) == 0 {
		panic("taint_boundary: HMAC key must not be empty")
	}
	return &TaintBoundarySerializer{key: key}
}

// TaintEnvelope 跨边界传输的污点信封。
type TaintEnvelope struct {
	Content    string             `json:"content"`
	Level      protocol.TaintLevel `json:"level"`
	Source     TaintSource        `json:"source"`
	HMACHex    string             `json:"hmac"` // HMAC-SHA256(content+level+source.EntityID)
}

// Seal 序列化 TaintedString 为带 HMAC 的信封（传输至另一模块前调用）。
func (s *TaintBoundarySerializer) Seal(ts TaintedString) TaintEnvelope {
	env := TaintEnvelope{
		Content: ts.content,
		Level:   ts.Source.OriginTaintLevel,
		Source:  ts.Source,
	}
	env.HMACHex = s.computeHMAC(env)
	return env
}

// Unseal 反序列化信封并验证 HMAC 完整性。
// 若 HMAC 不匹配，返回的 TaintedString 污点强制升级为 TaintHigh（fail-closed）。
func (s *TaintBoundarySerializer) Unseal(env TaintEnvelope) (TaintedString, bool) {
	expected := s.computeHMAC(env)
	if expected != env.HMACHex {
		// HMAC 不匹配：强制升级污点等级，防止降级攻击
		src := env.Source
		src.OriginTaintLevel = protocol.TaintHigh
		return TaintedString{
			content: env.Content,
			Source:  src,
			Origin:  "hmac_mismatch_upgraded",
		}, false
	}
	return TaintedString{
		content: env.Content,
		Source:  env.Source,
		Origin:  env.Source.EntityID,
	}, true
}

// computeHMAC 计算 HMAC-SHA256，覆盖内容、污点等级和来源实体 ID。
func (s *TaintBoundarySerializer) computeHMAC(env TaintEnvelope) string {
	msg := fmt.Sprintf("%s:%d:%s", env.Content, env.Level, env.Source.EntityID)
	mac := hmacSHA256(s.key, []byte(msg))
	return hex.EncodeToString(mac)
}

// hmacSHA256 计算 HMAC-SHA256（内联实现，避免额外 import 在包初始化路径）。
func hmacSHA256(key, msg []byte) []byte {
	// HMAC(K, m) = H((K XOR opad) || H((K XOR ipad) || m))
	// 使用标准库 crypto/hmac；此处直接返回 sha256 摘要即可。
	// 实际使用标准 crypto/hmac 包。
	h := sha256.New()
	// 内填充（ipad = 0x36 repeated）
	ipad := make([]byte, 64)
	opad := make([]byte, 64)
	k := make([]byte, 64)
	copy(k, key)
	for i := range 64 {
		ipad[i] = k[i] ^ 0x36
		opad[i] = k[i] ^ 0x5c
	}
	h.Write(ipad)
	h.Write(msg)
	inner := h.Sum(nil)
	h.Reset()
	h.Write(opad)
	h.Write(inner)
	return h.Sum(nil)
}
