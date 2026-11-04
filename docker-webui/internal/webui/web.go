package webui

import (
	"embed"
	"html/template"
	"io/fs"
	"net/http"
)

//go:embed web/templates/*.html web/static/*
var web embed.FS

var page = template.Must(template.ParseFS(web, "web/templates/index.html"))

func staticFiles() http.Handler {
	files, err := fs.Sub(web, "web/static")
	if err != nil {
		panic(err)
	}
	return http.StripPrefix("/static/", http.FileServer(http.FS(files)))
}
