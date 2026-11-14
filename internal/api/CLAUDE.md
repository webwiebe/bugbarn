# internal/api

## Routing

One `http.ServeMux`, built lazily by `Server.router()`. Every route is a pattern
(`"METHOD /path/{wildcard}"`) read with `r.PathValue`, registered in `routes_*.go`:

- `routes_ingest.go`: key-authenticated and CORS-open ingest paths. They answer before the session pipeline.
- `routes_public.go`: setup, health, login and OIDC. No session needed.
- `routes_internal.go`: HMAC-signed reader-to-writer endpoints. 404 until enabled, then 405 for non-POST.
- `routes_protected.go`: everything behind general CORS, `authenticate`, and (in a reader) `forwardWrites`. The `"/"` catch-all sits behind the same pipeline.
- `routes_middleware.go`: route flags (`flagAdminOnly`, `flagNoCSRF`) carried in the request context.

Rules:

- Register with `group.handle` for an API operation and `group.fallback` for anything that is not (preflights, wrong-method answers, `{$}` trailing-slash twins). `routePatterns()` lists operations only; `openapi_routes_test.go` compares them with `api/openapi.yaml`.
- An unauthenticated request to a protected path is 401, whatever the method or whether the path exists. Authenticated unknown paths are 404 and wrong methods 405 with `Allow`. Routing errors are plain text, as `http.Error` writes them.
- Some ingest paths answer a wrong method with 405 before authentication (`rejectMethods`, `methodNotAllowed`). List the methods explicitly: a method-less pattern overlaps wildcard patterns such as `GET /api/v1/setup/{slug...}` and panics at registration.
- A handler never re-checks the method or the path prefix. Use `r.Pattern` or a route flag.
- Add a protected GET route to `scopedReadRoutes` or `routesCoveredElsewhere` in `keybind_routes_test.go`, and to `routes_characterization.tsv` (`go test ./internal/api -run TestRouteCharacterization -update-routes`, then review the diff).
- Tests that call a handler directly have no `PathValue`; go through `server.ServeHTTP`.
