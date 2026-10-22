package agentapi

import (
	"context"
	"encoding/json"
	"reflect"

	"github.com/invopop/jsonschema"
)

// method is one entry of the catalog: the same structures serve the call
// and describe it (MCP tools/list).
type method struct {
	Name string
	Doc  string
	// Verb: the grant the method needs ("read", "logs", "edit",
	// "action:<id>" — the request's action), or "none".
	Verb      string
	Writes    bool
	Streaming bool
	req       reflect.Type
	resp      reflect.Type
	handle    func(ctx context.Context, c caller, body []byte) (any, error)
}

// register adds an agent operation to the MCP tool catalogue.
func register[Req, Resp any](s *Server, name, verb string, writes bool, doc string, fn func(ctx context.Context, c caller, req *Req) (*Resp, error)) {
	m := &method{
		Name: name, Doc: doc, Verb: verb, Writes: writes,
		req: reflect.TypeFor[Req](), resp: reflect.TypeFor[Resp](),
		handle: func(ctx context.Context, c caller, body []byte) (any, error) {
			var req Req
			if len(body) > 0 {
				if err := json.Unmarshal(body, &req); err != nil {
					return nil, badRequest("bad request body: %v", err)
				}
			}
			return fn(ctx, c, &req)
		},
	}
	s.methods = append(s.methods, m)
}

func (s *Server) registerMethods() {
	s.readMethods()
	s.writeMethods()
	s.execMethods()
}

// MethodDoc is one method of MCP tools/list.
type MethodDoc struct {
	Name      string             `json:"name"`
	Doc       string             `json:"description"`
	Verb      string             `json:"verb"`
	Writes    bool               `json:"writes"`
	Streaming bool               `json:"streaming,omitempty"`
	Request   *jsonschema.Schema `json:"request"`
	Response  *jsonschema.Schema `json:"response"`
}

type Catalog struct {
	Methods []MethodDoc `json:"methods"`
}

func (s *Server) catalog() Catalog {
	r := &jsonschema.Reflector{ExpandedStruct: true, DoNotReference: true, AllowAdditionalProperties: false}
	out := Catalog{Methods: make([]MethodDoc, 0, len(s.methods))}
	for _, m := range s.methods {
		request := r.ReflectFromType(m.req)
		if m.Name == "GetLogs" || m.Name == "StreamLogs" || m.Name == "ExportLogs" {
			request.DependentRequired = map[string][]string{"sinceTime": {"limit"}, "untilTime": {"limit"}}
		}
		if m.Name == "StreamLogs" {
			request.Required = append(request.Required, "limit")
		}
		out.Methods = append(out.Methods, MethodDoc{
			Name: m.Name, Doc: m.Doc, Verb: m.Verb, Writes: m.Writes, Streaming: m.Streaming,
			Request: request, Response: r.ReflectFromType(m.resp),
		})
	}
	return out
}
