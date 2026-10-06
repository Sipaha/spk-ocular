//go:build wails && linux && gtk3

package desktop

/*
#cgo pkg-config: gtk+-3.0
#include <gtk/gtk.h>

static GList* ocular_icon_append(GList* icons, const unsigned char* png, size_t len) {
    GBytes* bytes = g_bytes_new(png, len);
    GInputStream* stream = g_memory_input_stream_new_from_bytes(bytes);
    GdkPixbuf* pixbuf = gdk_pixbuf_new_from_stream(stream, NULL, NULL);
    g_object_unref(stream);
    g_bytes_unref(bytes);
    return pixbuf ? g_list_append(icons, pixbuf) : icons;
}
static void ocular_icons_apply(void* window, GList* icons) {
    if (window && icons) gtk_window_set_icon_list(GTK_WINDOW(window), icons);
    g_list_free_full(icons, g_object_unref);
}
*/
import "C"

import (
	"sync"
	"unsafe"

	"github.com/spk/spk-ocular/internal/appfiles"
	"github.com/wailsapp/wails/v3/pkg/application"
	"github.com/wailsapp/wails/v3/pkg/events"
)

func configureWindowIcons(window *application.WebviewWindow) {
	var once sync.Once
	window.OnWindowEvent(events.Common.WindowShow, func(*application.WindowEvent) {
		application.InvokeAsync(func() {
			native := window.NativeWindow()
			if native == nil {
				return
			}
			once.Do(func() {
				var icons *C.GList
				for _, icon := range appfiles.WindowIcons {
					if len(icon.PNG) == 0 {
						continue
					}
					// GBytes copies these Go bytes; no Go pointer survives the call.
					icons = C.ocular_icon_append(icons, (*C.uchar)(unsafe.Pointer(&icon.PNG[0])), C.size_t(len(icon.PNG)))
				}
				C.ocular_icons_apply(native, icons)
			})
		})
	})
}
