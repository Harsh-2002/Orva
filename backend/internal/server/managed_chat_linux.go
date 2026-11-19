//go:build linux

package server

import "net/http"

func registerManagedChat(mux *http.ServeMux, h *AIHandler) {
	for path, handle := range map[string]http.HandlerFunc{
		"POST /api/v1/ai/chat":                                   h.ChatStream,
		"GET /api/v1/ai/conversations":                           h.ListConversations,
		"POST /api/v1/ai/conversations":                          h.CreateConversation,
		"DELETE /api/v1/ai/conversations":                        h.DeleteAllConversations,
		"GET /api/v1/ai/conversations/{id}":                      h.GetConversation,
		"PATCH /api/v1/ai/conversations/{id}":                    h.PatchConversation,
		"DELETE /api/v1/ai/conversations/{id}":                   h.DeleteConversation,
		"GET /api/v1/ai/conversations/{id}/messages":             h.ListMessages,
		"POST /api/v1/ai/conversations/{id}/regenerate":          h.Regenerate,
		"POST /api/v1/ai/conversations/{id}/messages/{mid}/edit": h.EditMessage,
		"DELETE /api/v1/ai/conversations/{id}/messages/{mid}":    h.DeleteMessage,
		"POST /api/v1/ai/tool-calls/{id}/approve":                h.ApproveTool,
		"POST /api/v1/ai/tool-calls/{id}/reject":                 h.RejectTool,
		"GET /api/v1/ai/providers":                               h.ListProviders,
		"POST /api/v1/ai/providers":                              h.SaveProvider,
		"DELETE /api/v1/ai/providers/{id}":                       h.DeleteProvider,
		"GET /api/v1/ai/providers/{id}/models":                   h.ListProviderModels,
		"GET /api/v1/ai/settings":                                h.GetSettings,
		"PUT /api/v1/ai/settings":                                h.PutSettings,
		"PUT /api/v1/ai/selection":                               h.PutSelection,
	} {
		mux.HandleFunc(path, requireManagedOperator(h.DB, true, handle))
	}
}
