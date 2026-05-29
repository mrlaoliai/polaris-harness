package mcp

import (
	"os"
	"strings"
)

// secretKeyPatterns 是判断环境变量键名含有敏感信息的后缀列表（全大写比较）。
var secretKeyPatterns = []string{
	"_KEY", "_TOKEN", "_SECRET", "_PASSWORD", "_PASS",
	"_CREDENTIAL", "_CREDENTIALS", "_CRED", "_APIKEY",
	"_API_KEY", "_AUTH", "_PRIVATE",
}

// sanitizeParentEnv 返回过滤掉密钥类变量后的父进程环境切片。
// 保留 PATH、HOME、TMPDIR、USER、LANG、LC_ALL、TERM、NODE_PATH、GOPATH 等运行时必要变量；
// 移除任何键名匹配 secretKeyPatterns 的条目，防止凭据泄漏给 MCP 子进程。
func sanitizeParentEnv() []string {
	raw := os.Environ()
	out := make([]string, 0, len(raw))
	for _, kv := range raw {
		idx := strings.IndexByte(kv, '=')
		if idx <= 0 {
			out = append(out, kv)
			continue
		}
		key := strings.ToUpper(kv[:idx])
		if isSecretKey(key) {
			continue
		}
		out = append(out, kv)
	}
	return out
}

func isSecretKey(upperKey string) bool {
	for _, pat := range secretKeyPatterns {
		if strings.HasSuffix(upperKey, pat) {
			return true
		}
	}
	return false
}
