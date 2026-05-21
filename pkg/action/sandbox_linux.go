//go:build linux

package action

import (
	"syscall"

	"golang.org/x/sys/unix"
)

// containerSandboxSysProcAttr 返回 Linux 专属的子进程安全属性：
//   - CLONE_NEWPID: 独立 PID 命名空间，防止子进程枚举/信号攻击宿主
//   - CLONE_NEWNS:  独立挂载命名空间，防止子进程污染全局 mount 表
//   - Pdeathsig:   父进程退出时 SIGKILL 子进程，消灭孤儿进程
//
// Landlock LSM 文件系统白名单限制需要在子进程内调用 LandlockRestrictSelf，
// 须走 reexec 模式（polaris 二进制自检测 POLARIS_SANDBOX_EXEC 后 apply Landlock
// 再 exec 真实工具）——当前为 Tier0 MVP，以命名空间隔离为主要手段。
func containerSandboxSysProcAttr() *syscall.SysProcAttr {
	return &syscall.SysProcAttr{
		Cloneflags: unix.CLONE_NEWPID | unix.CLONE_NEWNS,
		Pdeathsig:  syscall.SIGKILL,
	}
}
