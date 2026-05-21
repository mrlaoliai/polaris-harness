//go:build !linux

package action

import "syscall"

// containerSandboxSysProcAttr 非 Linux 平台不支持 Landlock/CLONE_NEWPID，返回 nil。
// ContainerSandbox 在非 Linux 平台已通过 SandboxRouter 降级至 WasmSandbox，
// 此处仅作编译占位，正常运行路径不会到达。
func containerSandboxSysProcAttr() *syscall.SysProcAttr {
	return nil
}
