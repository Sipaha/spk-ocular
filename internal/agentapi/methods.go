package agentapi

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"reflect"

	"github.com/invopop/jsonschema"

	"github.com/spk/spk-ocular/internal/api"
)

// method is one entry of the catalog: the same structures serve the call
// and describe it (/v1/methods).
type method struct {
	Name string
	Doc  string
	// Verb: the grant the method needs ("read", "logs", "edit",
	// "action:<id>" — the request's action), or "none".
	Verb   string
	Writes bool
	req    reflect.Type
	resp   reflect.Type
	handle func(ctx context.Context, c caller, body []byte) (any, error)
}

// register adds a POST /v1/<name> method.
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
	s.mux.HandleFunc("POST /v1/"+name, func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxBody))
		if err != nil {
			writeError(w, badRequest("the request is over %d MiB", maxBody>>20))
			return
		}
		out, err := m.handle(r.Context(), caller{agent: agentName(r)}, body)
		if err != nil {
			writeError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, out)
	})
}

func (s *Server) routes() {
	s.mux.HandleFunc("GET /v1", func(w http.ResponseWriter, _ *http.Request) { writeJSON(w, http.StatusOK, s.intro()) })
	s.mux.HandleFunc("GET /v1/{$}", func(w http.ResponseWriter, _ *http.Request) { writeJSON(w, http.StatusOK, s.intro()) })
	s.mux.HandleFunc("GET /v1/methods", func(w http.ResponseWriter, _ *http.Request) { writeJSON(w, http.StatusOK, s.catalog()) })
	s.mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		writeError(w, &api.CodedError{Code: api.CodeNotFound, Detail: r.Method + " " + r.URL.Path + " is not a method: GET /v1/methods lists them"})
	})
	s.readMethods()
	s.writeMethods()
}

// Intro is GET /v1.
type Intro struct {
	Name    string   `json:"name"`
	Version string   `json:"version"`
	About   string   `json:"about"`
	Usage   []string `json:"usage"`
	Methods string   `json:"methods"`
}

func (s *Server) intro() Intro {
	return Intro{
		Name:    "SPK Ocular agent access",
		Version: s.o.Version,
		About: "Read and change Kubernetes and Docker Compose targets within what the user granted in SPK Ocular. " +
			"Nothing is granted by default; agents cannot grant themselves anything.",
		Usage: []string{
			"POST /v1/<Method> with a JSON body; GET /v1/methods lists the methods with their JSON schemas.",
			"Start with Access: the targets, namespaces, verbs and kinds granted to agents.",
			"Send your name in the " + agentHeader + " header: the user sees it (it is not verified).",
			"Changes are two steps: Prepare* returns the plan and a planId, Run* runs that plan.",
			"A destructive plan waits for the user's confirmation in the SPK Ocular window unless its grant says otherwise: " +
				"Run* then answers state awaiting_confirmation with a runId; ask GetRun (it waits up to 25 s) and tell your user to confirm in Ocular.",
			"Errors are {code, detail}: forbidden says what is not granted.",
		},
		Methods: "/v1/methods",
	}
}

// MethodDoc is one method of GET /v1/methods.
type MethodDoc struct {
	Name     string             `json:"name"`
	Path     string             `json:"path"`
	Doc      string             `json:"description"`
	Verb     string             `json:"verb"`
	Writes   bool               `json:"writes"`
	Request  *jsonschema.Schema `json:"request"`
	Response *jsonschema.Schema `json:"response"`
}

type Catalog struct {
	Methods []MethodDoc `json:"methods"`
}

func (s *Server) catalog() Catalog {
	r := &jsonschema.Reflector{ExpandedStruct: true, DoNotReference: true, AllowAdditionalProperties: false}
	out := Catalog{Methods: make([]MethodDoc, 0, len(s.methods))}
	for _, m := range s.methods {
		out.Methods = append(out.Methods, MethodDoc{
			Name: m.Name, Path: "/v1/" + m.Name, Doc: m.Doc, Verb: m.Verb, Writes: m.Writes,
			Request: r.ReflectFromType(m.req), Response: r.ReflectFromType(m.resp),
		})
	}
	return out
}
