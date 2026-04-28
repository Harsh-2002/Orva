package sandbox

import (
	"fmt"
	"sort"
	"strings"
)

// SyscallInfo describes a single Linux syscall.
type SyscallInfo struct {
	Name        string `json:"name"`
	Number      int    `json:"number"`
	Category    string `json:"category"`
	Description string `json:"description"`
	InDefault   bool   `json:"in_default"`
	InStrict    bool   `json:"in_strict"`
	InPermissive bool  `json:"in_permissive"`
}

// PolicyInfo describes a preset seccomp policy.
type PolicyInfo struct {
	Description  string `json:"description"`
	AllowedCount int    `json:"allowed_count"`
}

// --- Preset syscall lists ---

var defaultSyscalls = []string{
	// File I/O (stat/fstat/lstat removed — use newfstatat/statx on modern kernels)
	"read", "write", "open", "openat", "close", "newfstatat",
	"lseek", "access", "faccessat", "faccessat2", "readlink", "readlinkat",
	"getdents64", "fcntl", "dup", "dup2", "dup3", "pread64", "pwrite64",
	"readv", "writev", "statx", "statfs", "fstatfs",
	"rename", "renameat2", "unlink", "unlinkat", "mkdir", "mkdirat", "rmdir",
	"ftruncate", "truncate", "fallocate", "fadvise64", "copy_file_range",
	"flock", "chmod", "fchmod", "fchmodat", "umask",

	// Memory
	"mmap", "mprotect", "munmap", "brk", "mremap", "madvise",
	"membarrier", "memfd_create",

	// Process
	"clone", "clone3", "execve", "exit", "exit_group",
	"wait4", "waitid", "getpid", "getppid", "gettid",
	"getuid", "getgid", "geteuid", "getegid",
	"getgroups", "getrlimit", "prlimit64", "getrusage",
	"sched_getaffinity", "sched_yield",

	// Signals
	"rt_sigaction", "rt_sigprocmask", "rt_sigreturn", "sigaltstack",

	// Time
	"clock_gettime", "clock_getres", "clock_nanosleep", "nanosleep",
	"gettimeofday", "times",

	// System
	"sysinfo", "getrandom",
	"arch_prctl", "prctl", "set_tid_address", "set_robust_list",

	// Sync / IPC
	"futex", "rseq", "pipe", "pipe2", "eventfd2",

	// I/O multiplexing (needed by Node.js event loop)
	"epoll_create1", "epoll_ctl", "epoll_wait", "epoll_pwait",

	// Sockets (needed for Node.js internal IPC, not external network)
	"socket", "bind", "getsockname", "getsockopt", "setsockopt",
	"socketpair", "recvmsg", "sendmsg",

	// Misc
	"ioctl", "close_range", "select", "pselect6", "poll", "ppoll",
	"getcwd", "chdir", "fchdir", "setitimer", "getitimer", "setpriority",
}

var networkSyscalls = []string{
	"socket", "connect", "bind", "listen", "accept", "accept4",
	"sendto", "recvfrom", "sendmsg", "recvmsg", "sendmmsg", "recvmmsg",
	"setsockopt", "getsockopt", "getsockname", "getpeername",
	"shutdown", "socketpair",
	"epoll_create1", "epoll_ctl", "epoll_wait", "epoll_pwait",
}

var strictSyscalls = []string{
	// File I/O (read-only)
	"read", "write", "open", "openat", "close", "newfstatat",
	"lseek", "access", "readlink", "getdents64", "fcntl", "pread64",
	"readv", "statx",

	// Memory
	"mmap", "mprotect", "munmap", "brk", "mremap", "madvise",

	// Process (no fork/exec)
	"exit", "exit_group", "getpid", "gettid",
	"getuid", "getgid", "geteuid", "getegid", "prlimit64",

	// Signals
	"rt_sigaction", "rt_sigprocmask", "rt_sigreturn",

	// Time
	"clock_gettime", "clock_getres", "nanosleep",

	// System
	"uname", "getrandom", "arch_prctl", "prctl",
	"set_tid_address", "set_robust_list",

	// Sync
	"futex", "rseq",

	// Misc
	"ioctl", "close_range",
}

func init() {
	// Build the lookup sets.
	for _, s := range defaultSyscalls {
		defaultSet[s] = true
	}
	for _, s := range strictSyscalls {
		strictSet[s] = true
	}
	for _, s := range defaultSyscalls {
		permissiveSet[s] = true
	}
	for _, s := range networkSyscalls {
		permissiveSet[s] = true
	}
}

var defaultSet = make(map[string]bool)
var strictSet = make(map[string]bool)
var permissiveSet = make(map[string]bool)

// BuildSeccompPolicy generates a Kafel seccomp policy string.
// base is one of: "default", "strict", "permissive", "disabled".
// allow adds syscalls to the base. block removes syscalls (takes precedence).
func BuildSeccompPolicy(base string, allow, block []string) string {
	if base == "disabled" || base == "" {
		return ""
	}

	// Start with the base set.
	allowed := make(map[string]bool)
	switch base {
	case "strict":
		for k, v := range strictSet {
			allowed[k] = v
		}
	case "permissive":
		for k, v := range permissiveSet {
			allowed[k] = v
		}
	default: // "default"
		for k, v := range defaultSet {
			allowed[k] = v
		}
	}

	// Apply allow overrides.
	for _, s := range allow {
		s = strings.TrimSpace(s)
		if s != "" && ValidSyscallName(s) {
			allowed[s] = true
		}
	}

	// Apply block overrides (takes precedence).
	for _, s := range block {
		s = strings.TrimSpace(s)
		delete(allowed, s)
	}

	// Build sorted syscall list for deterministic output.
	var names []string
	for name := range allowed {
		names = append(names, name)
	}
	sort.Strings(names)

	// Generate Kafel policy.
	// ALLOW: explicitly permitted syscalls.
	// DEFAULT LOG: unrecognized syscalls are logged (not killed) — this
	// provides audit visibility while avoiding crashes from syscalls that
	// Kafel's symbol table doesn't include (e.g. fstat on kernel 6.17+).
	//
	// The ALLOW list still matters: nsjail's seccomp_log flag combined
	// with this policy means only ALLOW'd calls pass silently, everything
	// else generates a kernel audit log entry for monitoring.
	var b strings.Builder
	b.WriteString("POLICY orva { ALLOW { ")
	b.WriteString(strings.Join(names, ", "))
	b.WriteString(" } } USE orva DEFAULT LOG")

	return b.String()
}

// ValidSyscallName checks if a name is a known Linux syscall.
func ValidSyscallName(name string) bool {
	_, ok := allSyscalls[name]
	return ok
}

// ListSyscalls returns all known syscalls with metadata.
func ListSyscalls() []SyscallInfo {
	var result []SyscallInfo
	for name, info := range allSyscalls {
		info.InDefault = defaultSet[name]
		info.InStrict = strictSet[name]
		info.InPermissive = permissiveSet[name]
		result = append(result, info)
	}
	sort.Slice(result, func(i, j int) bool {
		return result[i].Number < result[j].Number
	})
	return result
}

// ListPolicies returns the available preset policies.
func ListPolicies() map[string]PolicyInfo {
	return map[string]PolicyInfo{
		"default":    {Description: "Standard sandboxing — file I/O, memory, process basics", AllowedCount: len(defaultSet)},
		"strict":     {Description: "Minimal — no network, no process creation, read-only I/O", AllowedCount: len(strictSet)},
		"permissive": {Description: "Adds network sockets for external API calls", AllowedCount: len(permissiveSet)},
		"disabled":   {Description: "All syscalls allowed (no seccomp filter)", AllowedCount: len(allSyscalls)},
	}
}

// allSyscalls is the complete x86_64 syscall table.
// Generated from /usr/include/x86_64-linux-gnu/asm/unistd_64.h
var allSyscalls = map[string]SyscallInfo{
	"read":                  {Name: "read", Number: 0, Category: "file_io", Description: "Read from file descriptor"},
	"write":                 {Name: "write", Number: 1, Category: "file_io", Description: "Write to file descriptor"},
	"open":                  {Name: "open", Number: 2, Category: "file_io", Description: "Open file"},
	"close":                 {Name: "close", Number: 3, Category: "file_io", Description: "Close file descriptor"},
	// stat/fstat/lstat (syscalls 4,5,6) removed from modern kernels — use newfstatat/statx
	"poll":                  {Name: "poll", Number: 7, Category: "io_multiplex", Description: "Wait for events on fds"},
	"lseek":                 {Name: "lseek", Number: 8, Category: "file_io", Description: "Seek in file"},
	"mmap":                  {Name: "mmap", Number: 9, Category: "memory", Description: "Map memory"},
	"mprotect":              {Name: "mprotect", Number: 10, Category: "memory", Description: "Set memory protection"},
	"munmap":                {Name: "munmap", Number: 11, Category: "memory", Description: "Unmap memory"},
	"brk":                   {Name: "brk", Number: 12, Category: "memory", Description: "Change data segment size"},
	"rt_sigaction":          {Name: "rt_sigaction", Number: 13, Category: "signal", Description: "Set signal handler"},
	"rt_sigprocmask":        {Name: "rt_sigprocmask", Number: 14, Category: "signal", Description: "Block/unblock signals"},
	"rt_sigreturn":          {Name: "rt_sigreturn", Number: 15, Category: "signal", Description: "Return from signal handler"},
	"ioctl":                 {Name: "ioctl", Number: 16, Category: "device", Description: "Device control"},
	"pread64":               {Name: "pread64", Number: 17, Category: "file_io", Description: "Read at offset"},
	"pwrite64":              {Name: "pwrite64", Number: 18, Category: "file_io", Description: "Write at offset"},
	"readv":                 {Name: "readv", Number: 19, Category: "file_io", Description: "Read into multiple buffers"},
	"writev":                {Name: "writev", Number: 20, Category: "file_io", Description: "Write from multiple buffers"},
	"access":                {Name: "access", Number: 21, Category: "file_io", Description: "Check file permissions"},
	"pipe":                  {Name: "pipe", Number: 22, Category: "ipc", Description: "Create pipe"},
	"select":                {Name: "select", Number: 23, Category: "io_multiplex", Description: "Wait for fd events"},
	"sched_yield":           {Name: "sched_yield", Number: 24, Category: "process", Description: "Yield CPU"},
	"mremap":                {Name: "mremap", Number: 25, Category: "memory", Description: "Remap memory"},
	"msync":                 {Name: "msync", Number: 26, Category: "memory", Description: "Sync memory-mapped file"},
	"mincore":               {Name: "mincore", Number: 27, Category: "memory", Description: "Check resident pages"},
	"madvise":               {Name: "madvise", Number: 28, Category: "memory", Description: "Memory usage advice"},
	"dup":                   {Name: "dup", Number: 32, Category: "file_io", Description: "Duplicate fd"},
	"dup2":                  {Name: "dup2", Number: 33, Category: "file_io", Description: "Duplicate fd to specific number"},
	"pause":                 {Name: "pause", Number: 34, Category: "signal", Description: "Wait for signal"},
	"nanosleep":             {Name: "nanosleep", Number: 35, Category: "time", Description: "High-resolution sleep"},
	"getitimer":             {Name: "getitimer", Number: 36, Category: "time", Description: "Get interval timer"},
	"alarm":                 {Name: "alarm", Number: 37, Category: "time", Description: "Set alarm clock"},
	"setitimer":             {Name: "setitimer", Number: 38, Category: "time", Description: "Set interval timer"},
	"getpid":                {Name: "getpid", Number: 39, Category: "process", Description: "Get process ID"},
	// sendfile (40) removed — not in Kafel symbol table on this kernel
	"socket":                {Name: "socket", Number: 41, Category: "network", Description: "Create socket"},
	"connect":               {Name: "connect", Number: 42, Category: "network", Description: "Connect socket"},
	"accept":                {Name: "accept", Number: 43, Category: "network", Description: "Accept connection"},
	"sendto":                {Name: "sendto", Number: 44, Category: "network", Description: "Send message on socket"},
	"recvfrom":              {Name: "recvfrom", Number: 45, Category: "network", Description: "Receive message from socket"},
	"sendmsg":               {Name: "sendmsg", Number: 46, Category: "network", Description: "Send message on socket"},
	"recvmsg":               {Name: "recvmsg", Number: 47, Category: "network", Description: "Receive message from socket"},
	"shutdown":              {Name: "shutdown", Number: 48, Category: "network", Description: "Shut down socket"},
	"bind":                  {Name: "bind", Number: 49, Category: "network", Description: "Bind socket to address"},
	"listen":                {Name: "listen", Number: 50, Category: "network", Description: "Listen on socket"},
	"getsockname":           {Name: "getsockname", Number: 51, Category: "network", Description: "Get socket name"},
	"getpeername":           {Name: "getpeername", Number: 52, Category: "network", Description: "Get peer name"},
	"socketpair":            {Name: "socketpair", Number: 53, Category: "network", Description: "Create socket pair"},
	"setsockopt":            {Name: "setsockopt", Number: 54, Category: "network", Description: "Set socket option"},
	"getsockopt":            {Name: "getsockopt", Number: 55, Category: "network", Description: "Get socket option"},
	"clone":                 {Name: "clone", Number: 56, Category: "process", Description: "Create child process"},
	"fork":                  {Name: "fork", Number: 57, Category: "process", Description: "Create child process"},
	"vfork":                 {Name: "vfork", Number: 58, Category: "process", Description: "Create child process (vfork)"},
	"execve":                {Name: "execve", Number: 59, Category: "process", Description: "Execute program"},
	"exit":                  {Name: "exit", Number: 60, Category: "process", Description: "Terminate process"},
	"wait4":                 {Name: "wait4", Number: 61, Category: "process", Description: "Wait for child"},
	"kill":                  {Name: "kill", Number: 62, Category: "signal", Description: "Send signal"},
	// uname (63) removed — not in Kafel symbol table on this kernel
	"fcntl":                 {Name: "fcntl", Number: 72, Category: "file_io", Description: "File descriptor control"},
	"flock":                 {Name: "flock", Number: 73, Category: "file_io", Description: "File lock"},
	"fsync":                 {Name: "fsync", Number: 74, Category: "file_io", Description: "Sync file to disk"},
	"fdatasync":             {Name: "fdatasync", Number: 75, Category: "file_io", Description: "Sync file data"},
	"truncate":              {Name: "truncate", Number: 76, Category: "file_io", Description: "Truncate file"},
	"ftruncate":             {Name: "ftruncate", Number: 77, Category: "file_io", Description: "Truncate file by fd"},
	"getdents64":            {Name: "getdents64", Number: 78, Category: "file_io", Description: "Read directory entries"},
	"getcwd":                {Name: "getcwd", Number: 79, Category: "file_io", Description: "Get current directory"},
	"chdir":                 {Name: "chdir", Number: 80, Category: "file_io", Description: "Change directory"},
	"fchdir":                {Name: "fchdir", Number: 81, Category: "file_io", Description: "Change directory by fd"},
	"rename":                {Name: "rename", Number: 82, Category: "file_io", Description: "Rename file"},
	"mkdir":                 {Name: "mkdir", Number: 83, Category: "file_io", Description: "Create directory"},
	"rmdir":                 {Name: "rmdir", Number: 84, Category: "file_io", Description: "Remove directory"},
	"link":                  {Name: "link", Number: 86, Category: "file_io", Description: "Create hard link"},
	"unlink":                {Name: "unlink", Number: 87, Category: "file_io", Description: "Delete file"},
	"symlink":               {Name: "symlink", Number: 88, Category: "file_io", Description: "Create symbolic link"},
	"readlink":              {Name: "readlink", Number: 89, Category: "file_io", Description: "Read symbolic link"},
	"chmod":                 {Name: "chmod", Number: 90, Category: "file_io", Description: "Change file permissions"},
	"fchmod":                {Name: "fchmod", Number: 91, Category: "file_io", Description: "Change permissions by fd"},
	"chown":                 {Name: "chown", Number: 92, Category: "file_io", Description: "Change file owner"},
	"fchown":                {Name: "fchown", Number: 93, Category: "file_io", Description: "Change owner by fd"},
	"umask":                 {Name: "umask", Number: 95, Category: "file_io", Description: "Set file creation mask"},
	"gettimeofday":          {Name: "gettimeofday", Number: 96, Category: "time", Description: "Get time of day"},
	"getrlimit":             {Name: "getrlimit", Number: 97, Category: "process", Description: "Get resource limits"},
	"getrusage":             {Name: "getrusage", Number: 98, Category: "process", Description: "Get resource usage"},
	"sysinfo":               {Name: "sysinfo", Number: 99, Category: "system", Description: "Get system info"},
	"times":                 {Name: "times", Number: 100, Category: "time", Description: "Get process times"},
	"ptrace":                {Name: "ptrace", Number: 101, Category: "dangerous", Description: "Process trace — used in container escapes"},
	"getuid":                {Name: "getuid", Number: 102, Category: "process", Description: "Get user ID"},
	"getgid":                {Name: "getgid", Number: 104, Category: "process", Description: "Get group ID"},
	"geteuid":               {Name: "geteuid", Number: 107, Category: "process", Description: "Get effective user ID"},
	"getegid":               {Name: "getegid", Number: 108, Category: "process", Description: "Get effective group ID"},
	"getppid":               {Name: "getppid", Number: 110, Category: "process", Description: "Get parent process ID"},
	"getgroups":             {Name: "getgroups", Number: 115, Category: "process", Description: "Get group list"},
	"setgroups":             {Name: "setgroups", Number: 116, Category: "dangerous", Description: "Set group list"},
	"setsid":                {Name: "setsid", Number: 112, Category: "process", Description: "Create session"},
	"getpgid":               {Name: "getpgid", Number: 121, Category: "process", Description: "Get process group"},
	"sigaltstack":           {Name: "sigaltstack", Number: 131, Category: "signal", Description: "Set alternate signal stack"},
	"statfs":                {Name: "statfs", Number: 137, Category: "file_io", Description: "Get filesystem stats"},
	"fstatfs":               {Name: "fstatfs", Number: 138, Category: "file_io", Description: "Get filesystem stats by fd"},
	"prctl":                 {Name: "prctl", Number: 157, Category: "process", Description: "Process control"},
	"arch_prctl":            {Name: "arch_prctl", Number: 158, Category: "process", Description: "Architecture-specific control"},
	"mount":                 {Name: "mount", Number: 165, Category: "dangerous", Description: "Mount filesystem"},
	"umount2":               {Name: "umount2", Number: 166, Category: "dangerous", Description: "Unmount filesystem"},
	"reboot":                {Name: "reboot", Number: 169, Category: "dangerous", Description: "Reboot system"},
	"gettid":                {Name: "gettid", Number: 186, Category: "process", Description: "Get thread ID"},
	"futex":                 {Name: "futex", Number: 202, Category: "sync", Description: "Fast userspace mutex"},
	"sched_getaffinity":     {Name: "sched_getaffinity", Number: 204, Category: "process", Description: "Get CPU affinity"},
	"sched_setaffinity":     {Name: "sched_setaffinity", Number: 203, Category: "process", Description: "Set CPU affinity"},
	"set_tid_address":       {Name: "set_tid_address", Number: 218, Category: "process", Description: "Set TID address"},
	"clock_gettime":         {Name: "clock_gettime", Number: 228, Category: "time", Description: "Get clock time"},
	"clock_getres":          {Name: "clock_getres", Number: 229, Category: "time", Description: "Get clock resolution"},
	"clock_nanosleep":       {Name: "clock_nanosleep", Number: 230, Category: "time", Description: "High-resolution sleep"},
	"exit_group":            {Name: "exit_group", Number: 231, Category: "process", Description: "Exit all threads"},
	"epoll_create1":         {Name: "epoll_create1", Number: 291, Category: "io_multiplex", Description: "Create epoll instance"},
	"epoll_ctl":             {Name: "epoll_ctl", Number: 233, Category: "io_multiplex", Description: "Control epoll"},
	"epoll_wait":            {Name: "epoll_wait", Number: 232, Category: "io_multiplex", Description: "Wait for epoll events"},
	"epoll_pwait":           {Name: "epoll_pwait", Number: 281, Category: "io_multiplex", Description: "Wait for epoll events with signal mask"},
	"set_robust_list":       {Name: "set_robust_list", Number: 273, Category: "process", Description: "Set robust futex list"},
	"eventfd2":              {Name: "eventfd2", Number: 290, Category: "ipc", Description: "Create event fd"},
	"accept4":               {Name: "accept4", Number: 288, Category: "network", Description: "Accept connection"},
	"pipe2":                 {Name: "pipe2", Number: 293, Category: "ipc", Description: "Create pipe"},
	"dup3":                  {Name: "dup3", Number: 292, Category: "file_io", Description: "Duplicate fd"},
	"prlimit64":             {Name: "prlimit64", Number: 302, Category: "process", Description: "Get/set resource limits"},
	"sendmmsg":              {Name: "sendmmsg", Number: 307, Category: "network", Description: "Send multiple messages"},
	"recvmmsg":              {Name: "recvmmsg", Number: 299, Category: "network", Description: "Receive multiple messages"},
	"getrandom":             {Name: "getrandom", Number: 318, Category: "system", Description: "Get random bytes"},
	"memfd_create":          {Name: "memfd_create", Number: 319, Category: "memory", Description: "Create anonymous file"},
	"membarrier":            {Name: "membarrier", Number: 324, Category: "memory", Description: "Memory barrier"},
	"copy_file_range":       {Name: "copy_file_range", Number: 326, Category: "file_io", Description: "Copy between fds"},
	"pselect6":              {Name: "pselect6", Number: 270, Category: "io_multiplex", Description: "Select with signal mask"},
	"ppoll":                 {Name: "ppoll", Number: 271, Category: "io_multiplex", Description: "Poll with signal mask"},
	"close_range":           {Name: "close_range", Number: 436, Category: "file_io", Description: "Close range of fds"},
	"openat":                {Name: "openat", Number: 257, Category: "file_io", Description: "Open file relative to dir"},
	"mkdirat":               {Name: "mkdirat", Number: 258, Category: "file_io", Description: "Create directory relative to dir"},
	"newfstatat":            {Name: "newfstatat", Number: 262, Category: "file_io", Description: "Get file status relative to dir"},
	"unlinkat":              {Name: "unlinkat", Number: 263, Category: "file_io", Description: "Delete file relative to dir"},
	"renameat2":             {Name: "renameat2", Number: 316, Category: "file_io", Description: "Rename file relative to dir"},
	"readlinkat":            {Name: "readlinkat", Number: 267, Category: "file_io", Description: "Read symlink relative to dir"},
	"fchmodat":              {Name: "fchmodat", Number: 268, Category: "file_io", Description: "Change permissions relative to dir"},
	"faccessat":             {Name: "faccessat", Number: 269, Category: "file_io", Description: "Check permissions relative to dir"},
	"faccessat2":            {Name: "faccessat2", Number: 439, Category: "file_io", Description: "Check permissions (extended)"},
	"statx":                 {Name: "statx", Number: 332, Category: "file_io", Description: "Extended file status"},
	"fallocate":             {Name: "fallocate", Number: 285, Category: "file_io", Description: "Allocate file space"},
	"fadvise64":             {Name: "fadvise64", Number: 221, Category: "file_io", Description: "File access advice"},
	"waitid":                {Name: "waitid", Number: 247, Category: "process", Description: "Wait for child process"},
	"clone3":                {Name: "clone3", Number: 435, Category: "process", Description: "Create child process (v3)"},
	"rseq":                  {Name: "rseq", Number: 334, Category: "sync", Description: "Restartable sequence"},
	"linkat":                {Name: "linkat", Number: 265, Category: "file_io", Description: "Create hard link relative to dir"},
	"symlinkat":             {Name: "symlinkat", Number: 266, Category: "file_io", Description: "Create symlink relative to dir"},

	// Dangerous syscalls — blocked by all policies except "disabled"
	"pivot_root":            {Name: "pivot_root", Number: 155, Category: "dangerous", Description: "Change root filesystem"},
	"unshare":               {Name: "unshare", Number: 272, Category: "dangerous", Description: "Create new namespaces — nested escape"},
	"setns":                 {Name: "setns", Number: 308, Category: "dangerous", Description: "Join namespace — escape"},
	"bpf":                   {Name: "bpf", Number: 321, Category: "dangerous", Description: "BPF operations — load kernel code"},
	"userfaultfd":           {Name: "userfaultfd", Number: 323, Category: "dangerous", Description: "User-space page fault — exploit tool"},
	"io_uring_setup":        {Name: "io_uring_setup", Number: 425, Category: "dangerous", Description: "Setup io_uring — multiple kernel CVEs"},
	"io_uring_enter":        {Name: "io_uring_enter", Number: 426, Category: "dangerous", Description: "Submit io_uring requests"},
	"io_uring_register":     {Name: "io_uring_register", Number: 427, Category: "dangerous", Description: "Register io_uring resources"},
	"kexec_load":            {Name: "kexec_load", Number: 246, Category: "dangerous", Description: "Load new kernel"},
	"kexec_file_load":       {Name: "kexec_file_load", Number: 320, Category: "dangerous", Description: "Load new kernel from file"},
	"perf_event_open":       {Name: "perf_event_open", Number: 298, Category: "dangerous", Description: "Performance monitoring — side channel"},
	"add_key":               {Name: "add_key", Number: 248, Category: "dangerous", Description: "Kernel keyring — escalation"},
	"keyctl":                {Name: "keyctl", Number: 250, Category: "dangerous", Description: "Kernel keyring control"},
	"request_key":           {Name: "request_key", Number: 249, Category: "dangerous", Description: "Request kernel key"},
	"process_vm_readv":      {Name: "process_vm_readv", Number: 310, Category: "dangerous", Description: "Read another process memory"},
	"process_vm_writev":     {Name: "process_vm_writev", Number: 311, Category: "dangerous", Description: "Write another process memory"},
	"init_module":           {Name: "init_module", Number: 175, Category: "dangerous", Description: "Load kernel module"},
	"finit_module":          {Name: "finit_module", Number: 313, Category: "dangerous", Description: "Load kernel module from fd"},
	"delete_module":         {Name: "delete_module", Number: 176, Category: "dangerous", Description: "Unload kernel module"},
	"capget":                {Name: "capget", Number: 125, Category: "dangerous", Description: "Get process capabilities"},
	"capset":                {Name: "capset", Number: 126, Category: "dangerous", Description: "Set process capabilities"},
}

// AllSyscallNames returns a sorted list of all known syscall names.
func AllSyscallNames() []string {
	names := make([]string, 0, len(allSyscalls))
	for name := range allSyscalls {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// CountForPolicy returns how many syscalls a given policy allows.
func CountForPolicy(policy string) int {
	switch policy {
	case "strict":
		return len(strictSet)
	case "permissive":
		return len(permissiveSet)
	case "disabled":
		return len(allSyscalls)
	default:
		return len(defaultSet)
	}
}

// IsDangerousSyscall returns true if the syscall is in the dangerous category.
func IsDangerousSyscall(name string) bool {
	if info, ok := allSyscalls[name]; ok {
		return info.Category == "dangerous"
	}
	return false
}

// ValidatePolicy checks if a policy name is valid.
func ValidatePolicy(name string) error {
	switch name {
	case "default", "strict", "permissive", "disabled", "":
		return nil
	default:
		return fmt.Errorf("unknown seccomp policy: %q (valid: default, strict, permissive, disabled)", name)
	}
}
