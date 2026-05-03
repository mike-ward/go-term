module github.com/mike-ward/go-term

go 1.26.0

require (
	github.com/creack/pty v1.1.24
	github.com/mike-ward/go-glyph v1.7.0
	github.com/mike-ward/go-gui v0.0.0-00010101000000-000000000000
	github.com/rivo/uniseg v0.4.7
)

require (
	github.com/alecthomas/chroma/v2 v2.23.1 // indirect
	github.com/dlclark/regexp2 v1.11.5 // indirect
	github.com/go-gl/gl v0.0.0-20260331235117-4566fea9a276 // indirect
	github.com/go-pdf/fpdf v0.9.0 // indirect
	github.com/godbus/dbus/v5 v5.2.2 // indirect
	github.com/tdewolff/parse/v2 v2.8.12 // indirect
	github.com/veandco/go-sdl2 v0.4.40 // indirect
	github.com/yuin/goldmark v1.8.2 // indirect
	github.com/yuin/goldmark-emoji v1.0.6 // indirect
	golang.org/x/sys v0.43.0 // indirect
)

replace (
	github.com/mike-ward/go-glyph => ../go-glyph
	github.com/mike-ward/go-gui => ../go-gui
)
