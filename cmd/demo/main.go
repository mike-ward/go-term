// Command demo runs the go-term widget in a single window.
package main

import (
	"log"

	"github.com/mike-ward/go-gui/gui"
	"github.com/mike-ward/go-gui/gui/backend"
	"github.com/mike-ward/go-term/term"
)

func main() {
	gui.SetTheme(gui.ThemeDarkBordered)

	var t *term.Term
	w := gui.NewWindow(gui.WindowCfg{
		Title:  "go-term",
		Width:  900,
		Height: 600,
		OnInit: func(w *gui.Window) {
			var err error
			t, err = term.New(w, term.Cfg{
				Themes: []term.NamedTheme{
					{Name: "Default", Theme: term.DefaultTheme},
					{Name: "Gruvbox", Theme: term.GruvboxTheme},
					{Name: "Nord", Theme: term.NordTheme},
					{Name: "Solarized Dark", Theme: term.SolarizedDarkTheme},
				},
			})
			if err != nil {
				log.Fatalf("term.New: %v", err)
			}
			w.UpdateView(t.View)
		},
	})
	defer func() {
		if t != nil {
			_ = t.Close()
		}
	}()
	backend.Run(w)
}
