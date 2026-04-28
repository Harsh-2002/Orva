package handlers

import (
	"net/http"

	"github.com/Harsh-2002/Orva/internal/sandbox"
	"github.com/Harsh-2002/Orva/internal/server/handlers/respond"
)

// SyscallHandler serves the syscall reference endpoint.
type SyscallHandler struct{}

// List handles GET /api/v1/syscalls — returns all syscalls with categories and policy info.
func (h *SyscallHandler) List(w http.ResponseWriter, r *http.Request) {
	respond.JSON(w, http.StatusOK, map[string]any{
		"total":    len(sandbox.ListSyscalls()),
		"policies": sandbox.ListPolicies(),
		"syscalls": sandbox.ListSyscalls(),
	})
}
