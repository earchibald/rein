package gateway

import "net/http"

type Route struct {
	Method  string
	Path    string
	Purpose string
}

type Stream struct {
	Path    string
	Event   string
	Purpose string
}

// V2Stub captures the intended HTTP and SSE shape for the future gateway
// without shipping transport behavior in this milestone.
type V2Stub struct {
	routes  []Route
	streams []Stream
}

func NewV2Stub() *V2Stub {
	return &V2Stub{
		routes: []Route{
			{Method: http.MethodGet, Path: "/v2/projects", Purpose: "list projects"},
			{Method: http.MethodGet, Path: "/v2/issues", Purpose: "list issues"},
			{Method: http.MethodGet, Path: "/v2/workflows", Purpose: "list workflows"},
		},
		streams: []Stream{
			{Path: "/v2/executions/stream", Event: "execution.updated", Purpose: "execution state updates"},
		},
	}
}

func (g *V2Stub) Handler() http.Handler {
	if g == nil {
		return http.NotFoundHandler()
	}

	notImplemented := func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "rein HTTP/SSE gateway is not implemented yet", http.StatusNotImplemented)
	}

	mux := http.NewServeMux()
	for _, route := range g.routes {
		mux.HandleFunc(route.Path, notImplemented)
	}
	for _, stream := range g.streams {
		mux.HandleFunc(stream.Path, notImplemented)
	}
	mux.HandleFunc("/v2", notImplemented)

	return mux
}

func (g *V2Stub) Routes() []Route {
	if g == nil {
		return nil
	}

	routes := make([]Route, len(g.routes))
	copy(routes, g.routes)
	return routes
}

func (g *V2Stub) Streams() []Stream {
	if g == nil {
		return nil
	}

	streams := make([]Stream, len(g.streams))
	copy(streams, g.streams)
	return streams
}
