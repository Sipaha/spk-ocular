//go:build wails

package transport

import (
	"reflect"
	"testing"
)

func TestEveryAPIMethodHasAWailsMethod(t *testing.T) {
	wails := reflect.TypeOf(&API{})
	for _, name := range apiMethods() {
		if _, ok := wails.MethodByName(name); !ok {
			t.Errorf("%s has no Wails method", name)
		}
	}
}
