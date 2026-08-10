package mcp

import (
	"context"

	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

// cacheScopeMiddleware rewrites the caching hint the 2026-07-28 protocol added
// to list and resource-read results.
//
// The SDK stamps every one of them with cacheScope "public"
// (Cacheable.setDefaultCacheableValues), and it does so on a result the SDK
// builds itself after the handler returns — so a tool handler cannot express
// anything different. "public" means any client OR INTERMEDIARY may cache the
// response and serve it to somebody else.
//
// That is wrong for every result Orva produces. The tool catalog is built per
// principal: registerFunctionTools and friends are gated on the caller's
// permission set, and a channel token sees only the functions bundled into that
// channel — one invoke-only tool each. Two callers hitting the same URL are
// entitled to different answers, so a shared cache entry is a cross-tenant
// disclosure of what other principals can do. Resource reads carry instance
// data and are no more shareable.
//
// TTLMs is deliberately left at the SDK's 0, which the spec defines as
// immediately stale. Orva's catalog changes whenever a function is deployed or
// deleted, a channel is edited, or a key's permissions change, and statelessness
// removed the session that a list-changed notification would have travelled
// over. A positive TTL would therefore be a promise Orva cannot keep: the
// correct answer is "ask again", and the win from caching a cheap local list is
// not worth serving a catalog that no longer matches the instance.
func cacheScopeMiddleware() mcpsdk.Middleware {
	return func(next mcpsdk.MethodHandler) mcpsdk.MethodHandler {
		return func(ctx context.Context, method string, req mcpsdk.Request) (mcpsdk.Result, error) {
			result, err := next(ctx, method, req)
			if err != nil {
				return result, err
			}
			// Every cacheable result type is handled explicitly rather than
			// through the CacheableResult interface, which is read-only
			// (GetCacheScope, no setter). A new cacheable result added by a
			// future SDK version will keep the "public" default here, so this
			// list is checked by TestEveryCacheableResultIsScopedPrivate.
			switch r := result.(type) {
			case *mcpsdk.ListToolsResult:
				r.CacheScope = cacheScopePrivate
			case *mcpsdk.ListResourcesResult:
				r.CacheScope = cacheScopePrivate
			case *mcpsdk.ListResourceTemplatesResult:
				r.CacheScope = cacheScopePrivate
			case *mcpsdk.ListPromptsResult:
				r.CacheScope = cacheScopePrivate
			case *mcpsdk.ReadResourceResult:
				r.CacheScope = cacheScopePrivate
			case *mcpsdk.DiscoverResult:
				r.CacheScope = cacheScopePrivate
			}
			return result, err
		}
	}
}

// cacheScopePrivate is the spec's value for "only the requesting user's client
// may cache this".
const cacheScopePrivate = "private"
