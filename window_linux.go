//go:build linux

package main

/*
#cgo pkg-config: gtk+-3.0
#include <gtk/gtk.h>

static void attach_headerbar(void *win_ptr) {
    GtkWidget *win = GTK_WIDGET(win_ptr);
    GtkWidget *header = gtk_header_bar_new();
    gtk_header_bar_set_title(GTK_HEADER_BAR(header), "Jellyfin");
    gtk_header_bar_set_show_close_button(GTK_HEADER_BAR(header), TRUE);
    gtk_header_bar_set_decoration_layout(GTK_HEADER_BAR(header), "menu:minimize,maximize,close");
    gtk_window_set_titlebar(GTK_WINDOW(win), header);
    gtk_widget_show_all(header);
}
*/
import "C"
import "unsafe"

func setupWindowDecorations(winPtr unsafe.Pointer) {
	C.attach_headerbar(winPtr)
}
