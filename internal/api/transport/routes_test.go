package transport

import (
	"net/http/httptest"
	"reflect"
	"testing"

	"github.com/spk/spk-ocular/internal/api"
	"github.com/spk/spk-ocular/internal/events"
)

// apiMethods are the names of the API interface's methods.
func apiMethods() []string {
	it := reflect.TypeOf((*api.API)(nil)).Elem()
	out := make([]string, 0, it.NumMethod())
	for i := 0; i < it.NumMethod(); i++ {
		out = append(out, it.Method(i).Name)
	}
	return out
}

// Every API method is reachable over HTTP (a method added to the interface
// and to Service but not routed would fail only in the running
// application); routes_wails_test.go checks the Wails side.
func TestEveryAPIMethodHasAnHTTPRoute(t *testing.T) {
	h := NewHTTP(&fakeAPI{}, events.NewEmitter())
	for _, name := range apiMethods() {
		if _, pattern := h.mux.Handler(httptest.NewRequest("POST", "/api/"+name, nil)); pattern == "" {
			t.Errorf("%s has no HTTP route", name)
		}
	}
}
