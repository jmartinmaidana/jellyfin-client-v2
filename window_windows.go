//go:build windows

package main

import "unsafe"

func setupWindowDecorations(winPtr unsafe.Pointer) {
	// En Windows, WebView2 utiliza la ventana nativa Win32 con barra de título y botón X integrados.
}
