package sandbox

import (
	"fmt"
	"runtime"
	"sort"
	"strings"
)

// SyscallInfo describes a single Linux syscall.
type SyscallInfo struct {
	Name         string `json:"name"`
	Number       int    `json:"number"`
	Category     string `json:"category"`
	Description  string `json:"description"`
	InDefault    bool   `json:"in_default"`
	InStrict     bool   `json:"in_strict"`
	InPermissive bool   `json:"in_permissive"`
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
	"clone", "execve", "exit", "exit_group",
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

// buildSyscalls is the allowlist for a dependency-install jail (see
// build.go). It is deliberately a superset of defaultSyscalls rather than a
// separate hand-rolled list: a build runs the same two language runtimes, it
// just also mutates the filesystem it was handed.
//
// Every entry below was chosen from a syscall trace of a real `npm install`
// (lodash + axios + typescript + esbuild, the last with a postinstall script)
// and a real `pip install` (requests + pydantic) executed inside the jail.
// The extras fall into four groups:
//
//   - Proven required — the install fails with EPERM without them:
//     symlink (npm writes node_modules/.bin links), listxattr and utimensat
//     (CPython's shutil.copystat, which pip uses for every installed file and
//     which does NOT tolerate EPERM from either call), fsync (pip's durable
//     write of each unpacked file).
//   - Architecture parity — glibc on aarch64 implements the legacy call via
//     its *at form, so the pair must be allowed together or the build breaks
//     on exactly one architecture: symlinkat, linkat, renameat. (renameat
//     matters most: the traced install renamed 617 times, and the base policy
//     carries only rename + renameat2, neither of which exists on aarch64.)
//   - Same-class companions with no new capability: link, fdatasync, preadv,
//     pwritev (libuv's positional I/O — 529 calls in the traced install), and
//     fork/vfork, which are strictly weaker than the clone already permitted.
//   - Contained process control: kill and tgkill. A postinstall script is
//     arbitrary code that spawns children; it lives in its own PID namespace,
//     so these can only signal the build's own processes. tgkill is also what
//     glibc's raise()/abort() uses.
//
// sched_getparam/sched_getscheduler are not the build's own calls: nsjail's
// NSTUN backend runs pthread_getschedparam on its threads, and denying it
// makes abseil spray "RAW: pthread_getschedparam failed" into build_logs.
//
// Network syscalls are NOT baked in — they are layered per step via
// SeccompAllowForNetworkMode, so the TypeScript compile step (which needs no
// network) does not get them.
var buildSyscalls = append(append([]string(nil), defaultSyscalls...),
	"symlink", "symlinkat", "link", "linkat", "renameat",
	"utimensat", "listxattr", "fsync", "fdatasync",
	"pwritev", "preadv",
	"fork", "vfork", "kill", "tgkill",
	"sched_getparam", "sched_getscheduler",
)

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
	for _, s := range buildSyscalls {
		buildSet[s] = true
	}
}

var defaultSet = make(map[string]bool)
var strictSet = make(map[string]bool)
var permissiveSet = make(map[string]bool)
var buildSet = make(map[string]bool)

// arm64 deliberately omits several legacy, non-*at syscalls and arch_prctl.
// Kafel treats an unknown syscall name as a policy compilation error, which
// makes nsjail exit before the adapter starts. Keep the human-facing catalog
// architecture-neutral, but remove unsupported identifiers from the policy
// compiled on ARM64. Orva release targets are currently amd64 and arm64 only.
var arm64UnsupportedSyscalls = map[string]bool{
	"access": true, "arch_prctl": true, "chmod": true, "dup2": true,
	"epoll_wait": true, "mkdir": true, "open": true, "pipe": true,
	"poll": true, "readlink": true, "rename": true, "rmdir": true,
	"select": true, "unlink": true,
	// Reached only via the build profile; aarch64's generic syscall table
	// has no non-*at link/symlink and no fork/vfork (glibc routes them
	// through linkat/symlinkat/clone), so naming them here would fail
	// Kafel compilation and take every build on arm64 down with it.
	"fork": true, "link": true, "symlink": true, "vfork": true,
}

// Kafel's amd64 and aarch64 catalogs use the kernel-internal names
// newfstat/newuname for the syscalls strace and libc expose as fstat/uname.
// They are not part of the API catalog below, but both current runtimes need
// them at startup. Add them after generic validation without exposing
// Kafel-specific names in the user-facing API.
var kafelRuntimeAliases = []string{"newfstat", "newuname"}

// Namespace-creating clone flags. Language runtimes need clone for threads,
// but a function has no reason to create a nested user/mount/network/etc.
// namespace. clone3 stores flags behind a pointer that classic seccomp BPF
// cannot inspect, so it stays denied with ENOSYS rather than the policy's
// default EPERM. glibc then falls back to clone, whose flags we can inspect.
// Keep this numeric so Kafel compiles the same policy on both targets.
const cloneNamespaceFlags = "0x7e020080"

func syscallSupportedOnArch(name, goarch string) bool {
	return goarch != "arm64" || !arm64UnsupportedSyscalls[name]
}

// SeccompAllowForNetworkMode returns the extra syscalls a sandbox needs when the
// operator has granted it outbound network access.
//
// The base policies deliberately permit only the socket calls Node's internal
// IPC needs — `connect` is NOT among them (see defaultSyscalls). Those calls
// live in networkSyscalls, which previously reached a sandbox only by switching
// the entire instance to the `permissive` base via ORVA_SECCOMP_POLICY.
//
// That made network_mode=egress a per-function toggle whose effect depended on a
// global env var: flipping it looked like it worked, but the function could not
// open an outbound connection at all, and seccomp killed the attempt long before
// the egress policy was consulted. Granting these syscalls per function, driven
// by the same field that grants the network namespace, is what makes the toggle
// mean what it says. What the function may then *reach* is decided by the
// compiled NSTUN egress policy, which is the control that belongs in that role.
func SeccompAllowForNetworkMode(mode string) []string {
	if mode != "egress" {
		return nil
	}
	return append([]string(nil), networkSyscalls...)
}

// BuildSeccompPolicy generates a Kafel seccomp policy string.
// base is one of: "default", "strict", "permissive", "disabled".
// allow adds syscalls to the base. block removes syscalls (takes precedence).
func BuildSeccompPolicy(base string, allow, block []string) string {
	return buildSeccompPolicyForArch(base, allow, block, runtime.GOARCH)
}

// seccompBaseBuild names the dependency-install profile. It is deliberately
// absent from ValidatePolicy/ListPolicies: it is chosen by RunBuild for build
// jails, never by an operator for worker sandboxes (ORVA_SECCOMP_POLICY is
// validated against the four public names, so this one cannot be selected
// there even by typo).
const seccompBaseBuild = "build"

// BuildJailSeccompPolicy returns the Kafel policy a build jail runs under.
// mode is the build step's network mode, so the outbound socket syscalls are
// granted to the dependency installs that need a registry and withheld from
// the compile step that does not.
func BuildJailSeccompPolicy(mode string) string {
	return BuildSeccompPolicy(seccompBaseBuild, SeccompAllowForNetworkMode(mode), nil)
}

func buildSeccompPolicyForArch(base string, allow, block []string, goarch string) string {
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
	case seccompBaseBuild:
		for k, v := range buildSet {
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

	// Filter after custom allow overrides as well, otherwise an operator can
	// accidentally reintroduce an identifier Kafel cannot compile here.
	for name := range allowed {
		if !ValidSyscallName(name) || !syscallSupportedOnArch(name, goarch) {
			delete(allowed, name)
		}
	}
	for _, name := range kafelRuntimeAliases {
		allowed[name] = true
	}

	// Build sorted syscall list for deterministic output.
	var names []string
	for name := range allowed {
		names = append(names, name)
	}
	sort.Strings(names)

	// Generate Kafel policy.
	// ALLOW: explicitly permitted syscalls.
	// DEFAULT ERRNO(1) makes this an enforcing allowlist while allowing
	// language runtimes to fall back when optional kernel features such as
	// io_uring are denied. DEFAULT LOG is not a restriction: Linux logs the
	// syscall and then permits it to execute.
	var b strings.Builder
	// pthread_create on current amd64 glibc tries clone3 first and falls back
	// to clone only for ENOSYS. Returning the default EPERM makes Node abort at
	// startup; allowing clone3 would let a function hide namespace flags behind
	// its pointer argument. An explicit ENOSYS rule preserves both compatibility
	// and the clone flag boundary below.
	b.WriteString("POLICY orva { ERRNO(38) { clone3 }, ALLOW { ")
	var rules []string
	for _, name := range names {
		if name == "clone" {
			// Declare arg0 ourselves instead of relying on Kafel's
			// architecture-specific catalog name (flags on x86_64,
			// clone_flags on aarch64).
			rules = append(rules, "clone(orva_clone_flags) { (orva_clone_flags & "+cloneNamespaceFlags+") == 0 }")
			continue
		}
		rules = append(rules, name)
	}
	b.WriteString(strings.Join(rules, ", "))
	b.WriteString(" } } USE orva DEFAULT ERRNO(1)")

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
	"read":  {Name: "read", Number: 0, Category: "file_io", Description: "Read from file descriptor"},
	"write": {Name: "write", Number: 1, Category: "file_io", Description: "Write to file descriptor"},
	"open":  {Name: "open", Number: 2, Category: "file_io", Description: "Open file"},
	"close": {Name: "close", Number: 3, Category: "file_io", Description: "Close file descriptor"},
	// stat/fstat/lstat (syscalls 4,5,6) removed from modern kernels — use newfstatat/statx
	"poll":           {Name: "poll", Number: 7, Category: "io_multiplex", Description: "Wait for events on fds"},
	"lseek":          {Name: "lseek", Number: 8, Category: "file_io", Description: "Seek in file"},
	"mmap":           {Name: "mmap", Number: 9, Category: "memory", Description: "Map memory"},
	"mprotect":       {Name: "mprotect", Number: 10, Category: "memory", Description: "Set memory protection"},
	"munmap":         {Name: "munmap", Number: 11, Category: "memory", Description: "Unmap memory"},
	"brk":            {Name: "brk", Number: 12, Category: "memory", Description: "Change data segment size"},
	"rt_sigaction":   {Name: "rt_sigaction", Number: 13, Category: "signal", Description: "Set signal handler"},
	"rt_sigprocmask": {Name: "rt_sigprocmask", Number: 14, Category: "signal", Description: "Block/unblock signals"},
	"rt_sigreturn":   {Name: "rt_sigreturn", Number: 15, Category: "signal", Description: "Return from signal handler"},
	"ioctl":          {Name: "ioctl", Number: 16, Category: "device", Description: "Device control"},
	"pread64":        {Name: "pread64", Number: 17, Category: "file_io", Description: "Read at offset"},
	"pwrite64":       {Name: "pwrite64", Number: 18, Category: "file_io", Description: "Write at offset"},
	"readv":          {Name: "readv", Number: 19, Category: "file_io", Description: "Read into multiple buffers"},
	"writev":         {Name: "writev", Number: 20, Category: "file_io", Description: "Write from multiple buffers"},
	"access":         {Name: "access", Number: 21, Category: "file_io", Description: "Check file permissions"},
	"pipe":           {Name: "pipe", Number: 22, Category: "ipc", Description: "Create pipe"},
	"select":         {Name: "select", Number: 23, Category: "io_multiplex", Description: "Wait for fd events"},
	"sched_yield":    {Name: "sched_yield", Number: 24, Category: "process", Description: "Yield CPU"},
	"mremap":         {Name: "mremap", Number: 25, Category: "memory", Description: "Remap memory"},
	"msync":          {Name: "msync", Number: 26, Category: "memory", Description: "Sync memory-mapped file"},
	"mincore":        {Name: "mincore", Number: 27, Category: "memory", Description: "Check resident pages"},
	"madvise":        {Name: "madvise", Number: 28, Category: "memory", Description: "Memory usage advice"},
	"dup":            {Name: "dup", Number: 32, Category: "file_io", Description: "Duplicate fd"},
	"dup2":           {Name: "dup2", Number: 33, Category: "file_io", Description: "Duplicate fd to specific number"},
	"pause":          {Name: "pause", Number: 34, Category: "signal", Description: "Wait for signal"},
	"nanosleep":      {Name: "nanosleep", Number: 35, Category: "time", Description: "High-resolution sleep"},
	"getitimer":      {Name: "getitimer", Number: 36, Category: "time", Description: "Get interval timer"},
	"alarm":          {Name: "alarm", Number: 37, Category: "time", Description: "Set alarm clock"},
	"setitimer":      {Name: "setitimer", Number: 38, Category: "time", Description: "Set interval timer"},
	"getpid":         {Name: "getpid", Number: 39, Category: "process", Description: "Get process ID"},
	// sendfile (40) removed — not in Kafel symbol table on this kernel
	"socket":      {Name: "socket", Number: 41, Category: "network", Description: "Create socket"},
	"connect":     {Name: "connect", Number: 42, Category: "network", Description: "Connect socket"},
	"accept":      {Name: "accept", Number: 43, Category: "network", Description: "Accept connection"},
	"sendto":      {Name: "sendto", Number: 44, Category: "network", Description: "Send message on socket"},
	"recvfrom":    {Name: "recvfrom", Number: 45, Category: "network", Description: "Receive message from socket"},
	"sendmsg":     {Name: "sendmsg", Number: 46, Category: "network", Description: "Send message on socket"},
	"recvmsg":     {Name: "recvmsg", Number: 47, Category: "network", Description: "Receive message from socket"},
	"shutdown":    {Name: "shutdown", Number: 48, Category: "network", Description: "Shut down socket"},
	"bind":        {Name: "bind", Number: 49, Category: "network", Description: "Bind socket to address"},
	"listen":      {Name: "listen", Number: 50, Category: "network", Description: "Listen on socket"},
	"getsockname": {Name: "getsockname", Number: 51, Category: "network", Description: "Get socket name"},
	"getpeername": {Name: "getpeername", Number: 52, Category: "network", Description: "Get peer name"},
	"socketpair":  {Name: "socketpair", Number: 53, Category: "network", Description: "Create socket pair"},
	"setsockopt":  {Name: "setsockopt", Number: 54, Category: "network", Description: "Set socket option"},
	"getsockopt":  {Name: "getsockopt", Number: 55, Category: "network", Description: "Get socket option"},
	"clone":       {Name: "clone", Number: 56, Category: "process", Description: "Create child process"},
	"fork":        {Name: "fork", Number: 57, Category: "process", Description: "Create child process"},
	"vfork":       {Name: "vfork", Number: 58, Category: "process", Description: "Create child process (vfork)"},
	"execve":      {Name: "execve", Number: 59, Category: "process", Description: "Execute program"},
	"exit":        {Name: "exit", Number: 60, Category: "process", Description: "Terminate process"},
	"wait4":       {Name: "wait4", Number: 61, Category: "process", Description: "Wait for child"},
	"kill":        {Name: "kill", Number: 62, Category: "signal", Description: "Send signal"},
	// uname (63) removed — not in Kafel symbol table on this kernel
	"fcntl":             {Name: "fcntl", Number: 72, Category: "file_io", Description: "File descriptor control"},
	"flock":             {Name: "flock", Number: 73, Category: "file_io", Description: "File lock"},
	"fsync":             {Name: "fsync", Number: 74, Category: "file_io", Description: "Sync file to disk"},
	"fdatasync":         {Name: "fdatasync", Number: 75, Category: "file_io", Description: "Sync file data"},
	"truncate":          {Name: "truncate", Number: 76, Category: "file_io", Description: "Truncate file"},
	"ftruncate":         {Name: "ftruncate", Number: 77, Category: "file_io", Description: "Truncate file by fd"},
	"getdents64":        {Name: "getdents64", Number: 78, Category: "file_io", Description: "Read directory entries"},
	"getcwd":            {Name: "getcwd", Number: 79, Category: "file_io", Description: "Get current directory"},
	"chdir":             {Name: "chdir", Number: 80, Category: "file_io", Description: "Change directory"},
	"fchdir":            {Name: "fchdir", Number: 81, Category: "file_io", Description: "Change directory by fd"},
	"rename":            {Name: "rename", Number: 82, Category: "file_io", Description: "Rename file"},
	"mkdir":             {Name: "mkdir", Number: 83, Category: "file_io", Description: "Create directory"},
	"rmdir":             {Name: "rmdir", Number: 84, Category: "file_io", Description: "Remove directory"},
	"link":              {Name: "link", Number: 86, Category: "file_io", Description: "Create hard link"},
	"unlink":            {Name: "unlink", Number: 87, Category: "file_io", Description: "Delete file"},
	"symlink":           {Name: "symlink", Number: 88, Category: "file_io", Description: "Create symbolic link"},
	"readlink":          {Name: "readlink", Number: 89, Category: "file_io", Description: "Read symbolic link"},
	"chmod":             {Name: "chmod", Number: 90, Category: "file_io", Description: "Change file permissions"},
	"fchmod":            {Name: "fchmod", Number: 91, Category: "file_io", Description: "Change permissions by fd"},
	"chown":             {Name: "chown", Number: 92, Category: "file_io", Description: "Change file owner"},
	"fchown":            {Name: "fchown", Number: 93, Category: "file_io", Description: "Change owner by fd"},
	"umask":             {Name: "umask", Number: 95, Category: "file_io", Description: "Set file creation mask"},
	"gettimeofday":      {Name: "gettimeofday", Number: 96, Category: "time", Description: "Get time of day"},
	"getrlimit":         {Name: "getrlimit", Number: 97, Category: "process", Description: "Get resource limits"},
	"getrusage":         {Name: "getrusage", Number: 98, Category: "process", Description: "Get resource usage"},
	"sysinfo":           {Name: "sysinfo", Number: 99, Category: "system", Description: "Get system info"},
	"times":             {Name: "times", Number: 100, Category: "time", Description: "Get process times"},
	"ptrace":            {Name: "ptrace", Number: 101, Category: "dangerous", Description: "Process trace — used in container escapes"},
	"getuid":            {Name: "getuid", Number: 102, Category: "process", Description: "Get user ID"},
	"getgid":            {Name: "getgid", Number: 104, Category: "process", Description: "Get group ID"},
	"geteuid":           {Name: "geteuid", Number: 107, Category: "process", Description: "Get effective user ID"},
	"getegid":           {Name: "getegid", Number: 108, Category: "process", Description: "Get effective group ID"},
	"getppid":           {Name: "getppid", Number: 110, Category: "process", Description: "Get parent process ID"},
	"getgroups":         {Name: "getgroups", Number: 115, Category: "process", Description: "Get group list"},
	"setgroups":         {Name: "setgroups", Number: 116, Category: "dangerous", Description: "Set group list"},
	"setsid":            {Name: "setsid", Number: 112, Category: "process", Description: "Create session"},
	"getpgid":           {Name: "getpgid", Number: 121, Category: "process", Description: "Get process group"},
	"sigaltstack":       {Name: "sigaltstack", Number: 131, Category: "signal", Description: "Set alternate signal stack"},
	"statfs":            {Name: "statfs", Number: 137, Category: "file_io", Description: "Get filesystem stats"},
	"fstatfs":           {Name: "fstatfs", Number: 138, Category: "file_io", Description: "Get filesystem stats by fd"},
	"prctl":             {Name: "prctl", Number: 157, Category: "process", Description: "Process control"},
	"arch_prctl":        {Name: "arch_prctl", Number: 158, Category: "process", Description: "Architecture-specific control"},
	"mount":             {Name: "mount", Number: 165, Category: "dangerous", Description: "Mount filesystem"},
	"umount2":           {Name: "umount2", Number: 166, Category: "dangerous", Description: "Unmount filesystem"},
	"reboot":            {Name: "reboot", Number: 169, Category: "dangerous", Description: "Reboot system"},
	"gettid":            {Name: "gettid", Number: 186, Category: "process", Description: "Get thread ID"},
	"futex":             {Name: "futex", Number: 202, Category: "sync", Description: "Fast userspace mutex"},
	"sched_getaffinity": {Name: "sched_getaffinity", Number: 204, Category: "process", Description: "Get CPU affinity"},
	"sched_setaffinity": {Name: "sched_setaffinity", Number: 203, Category: "process", Description: "Set CPU affinity"},
	"set_tid_address":   {Name: "set_tid_address", Number: 218, Category: "process", Description: "Set TID address"},
	"clock_gettime":     {Name: "clock_gettime", Number: 228, Category: "time", Description: "Get clock time"},
	"clock_getres":      {Name: "clock_getres", Number: 229, Category: "time", Description: "Get clock resolution"},
	"clock_nanosleep":   {Name: "clock_nanosleep", Number: 230, Category: "time", Description: "High-resolution sleep"},
	"exit_group":        {Name: "exit_group", Number: 231, Category: "process", Description: "Exit all threads"},
	"epoll_create1":     {Name: "epoll_create1", Number: 291, Category: "io_multiplex", Description: "Create epoll instance"},
	"epoll_ctl":         {Name: "epoll_ctl", Number: 233, Category: "io_multiplex", Description: "Control epoll"},
	"epoll_wait":        {Name: "epoll_wait", Number: 232, Category: "io_multiplex", Description: "Wait for epoll events"},
	"epoll_pwait":       {Name: "epoll_pwait", Number: 281, Category: "io_multiplex", Description: "Wait for epoll events with signal mask"},
	"set_robust_list":   {Name: "set_robust_list", Number: 273, Category: "process", Description: "Set robust futex list"},
	"eventfd2":          {Name: "eventfd2", Number: 290, Category: "ipc", Description: "Create event fd"},
	"accept4":           {Name: "accept4", Number: 288, Category: "network", Description: "Accept connection"},
	"pipe2":             {Name: "pipe2", Number: 293, Category: "ipc", Description: "Create pipe"},
	"dup3":              {Name: "dup3", Number: 292, Category: "file_io", Description: "Duplicate fd"},
	"prlimit64":         {Name: "prlimit64", Number: 302, Category: "process", Description: "Get/set resource limits"},
	"sendmmsg":          {Name: "sendmmsg", Number: 307, Category: "network", Description: "Send multiple messages"},
	"recvmmsg":          {Name: "recvmmsg", Number: 299, Category: "network", Description: "Receive multiple messages"},
	"getrandom":         {Name: "getrandom", Number: 318, Category: "system", Description: "Get random bytes"},
	"memfd_create":      {Name: "memfd_create", Number: 319, Category: "memory", Description: "Create anonymous file"},
	"membarrier":        {Name: "membarrier", Number: 324, Category: "memory", Description: "Memory barrier"},
	"copy_file_range":   {Name: "copy_file_range", Number: 326, Category: "file_io", Description: "Copy between fds"},
	"pselect6":          {Name: "pselect6", Number: 270, Category: "io_multiplex", Description: "Select with signal mask"},
	"ppoll":             {Name: "ppoll", Number: 271, Category: "io_multiplex", Description: "Poll with signal mask"},
	"close_range":       {Name: "close_range", Number: 436, Category: "file_io", Description: "Close range of fds"},
	"openat":            {Name: "openat", Number: 257, Category: "file_io", Description: "Open file relative to dir"},
	"mkdirat":           {Name: "mkdirat", Number: 258, Category: "file_io", Description: "Create directory relative to dir"},
	"newfstatat":        {Name: "newfstatat", Number: 262, Category: "file_io", Description: "Get file status relative to dir"},
	"unlinkat":          {Name: "unlinkat", Number: 263, Category: "file_io", Description: "Delete file relative to dir"},
	"renameat2":         {Name: "renameat2", Number: 316, Category: "file_io", Description: "Rename file relative to dir"},
	"readlinkat":        {Name: "readlinkat", Number: 267, Category: "file_io", Description: "Read symlink relative to dir"},
	"fchmodat":          {Name: "fchmodat", Number: 268, Category: "file_io", Description: "Change permissions relative to dir"},
	"faccessat":         {Name: "faccessat", Number: 269, Category: "file_io", Description: "Check permissions relative to dir"},
	"faccessat2":        {Name: "faccessat2", Number: 439, Category: "file_io", Description: "Check permissions (extended)"},
	"statx":             {Name: "statx", Number: 332, Category: "file_io", Description: "Extended file status"},
	"fallocate":         {Name: "fallocate", Number: 285, Category: "file_io", Description: "Allocate file space"},
	"fadvise64":         {Name: "fadvise64", Number: 221, Category: "file_io", Description: "File access advice"},
	"waitid":            {Name: "waitid", Number: 247, Category: "process", Description: "Wait for child process"},
	"clone3":            {Name: "clone3", Number: 435, Category: "process", Description: "Create child process (v3)"},
	"rseq":              {Name: "rseq", Number: 334, Category: "sync", Description: "Restartable sequence"},
	"linkat":            {Name: "linkat", Number: 265, Category: "file_io", Description: "Create hard link relative to dir"},
	"symlinkat":         {Name: "symlinkat", Number: 266, Category: "file_io", Description: "Create symlink relative to dir"},

	// Reached only through the build profile (see buildSyscalls). Listed here
	// because BuildSeccompPolicy drops any name this catalog does not know.
	"renameat":           {Name: "renameat", Number: 264, Category: "file_io", Description: "Rename file relative to dir"},
	"utimensat":          {Name: "utimensat", Number: 280, Category: "file_io", Description: "Set file timestamps"},
	"listxattr":          {Name: "listxattr", Number: 194, Category: "file_io", Description: "List extended attributes"},
	"preadv":             {Name: "preadv", Number: 295, Category: "file_io", Description: "Read into multiple buffers at offset"},
	"pwritev":            {Name: "pwritev", Number: 296, Category: "file_io", Description: "Write from multiple buffers at offset"},
	"tgkill":             {Name: "tgkill", Number: 234, Category: "signal", Description: "Send signal to a thread"},
	"sched_getparam":     {Name: "sched_getparam", Number: 143, Category: "process", Description: "Get scheduling parameters"},
	"sched_getscheduler": {Name: "sched_getscheduler", Number: 145, Category: "process", Description: "Get scheduling policy"},

	// Dangerous syscalls — blocked by all policies except "disabled"
	"pivot_root":        {Name: "pivot_root", Number: 155, Category: "dangerous", Description: "Change root filesystem"},
	"unshare":           {Name: "unshare", Number: 272, Category: "dangerous", Description: "Create new namespaces — nested escape"},
	"setns":             {Name: "setns", Number: 308, Category: "dangerous", Description: "Join namespace — escape"},
	"bpf":               {Name: "bpf", Number: 321, Category: "dangerous", Description: "BPF operations — load kernel code"},
	"userfaultfd":       {Name: "userfaultfd", Number: 323, Category: "dangerous", Description: "User-space page fault — exploit tool"},
	"io_uring_setup":    {Name: "io_uring_setup", Number: 425, Category: "dangerous", Description: "Setup io_uring — multiple kernel CVEs"},
	"io_uring_enter":    {Name: "io_uring_enter", Number: 426, Category: "dangerous", Description: "Submit io_uring requests"},
	"io_uring_register": {Name: "io_uring_register", Number: 427, Category: "dangerous", Description: "Register io_uring resources"},
	"kexec_load":        {Name: "kexec_load", Number: 246, Category: "dangerous", Description: "Load new kernel"},
	"kexec_file_load":   {Name: "kexec_file_load", Number: 320, Category: "dangerous", Description: "Load new kernel from file"},
	"perf_event_open":   {Name: "perf_event_open", Number: 298, Category: "dangerous", Description: "Performance monitoring — side channel"},
	"add_key":           {Name: "add_key", Number: 248, Category: "dangerous", Description: "Kernel keyring — escalation"},
	"keyctl":            {Name: "keyctl", Number: 250, Category: "dangerous", Description: "Kernel keyring control"},
	"request_key":       {Name: "request_key", Number: 249, Category: "dangerous", Description: "Request kernel key"},
	"process_vm_readv":  {Name: "process_vm_readv", Number: 310, Category: "dangerous", Description: "Read another process memory"},
	"process_vm_writev": {Name: "process_vm_writev", Number: 311, Category: "dangerous", Description: "Write another process memory"},
	"init_module":       {Name: "init_module", Number: 175, Category: "dangerous", Description: "Load kernel module"},
	"finit_module":      {Name: "finit_module", Number: 313, Category: "dangerous", Description: "Load kernel module from fd"},
	"delete_module":     {Name: "delete_module", Number: 176, Category: "dangerous", Description: "Unload kernel module"},
	"capget":            {Name: "capget", Number: 125, Category: "dangerous", Description: "Get process capabilities"},
	"capset":            {Name: "capset", Number: 126, Category: "dangerous", Description: "Set process capabilities"},
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
