package web // import "github.com/cafebazaar/blacksmith/web"

import (
	"net"
	"net/http"
	"os"
	"path/filepath"

	"github.com/gorilla/handlers"
	"github.com/gorilla/mux"

	"github.com/cafebazaar/blacksmith/datasource"
)

type webServer struct {
	ds datasource.DataSource
}

// Handler uses a multiplexing router to route http requests
func (ws *webServer) Handler() http.Handler {
	mux := mux.NewRouter()

	mux.PathPrefix("/t/cc/").HandlerFunc(ws.Cloudconfig).Methods("GET")
	mux.PathPrefix("/t/ig/").HandlerFunc(ws.Ignition).Methods("GET")
	mux.PathPrefix("/t/bp/").HandlerFunc(ws.Bootparams).Methods("GET")

	mux.HandleFunc("/api/version", ws.Version)

	mux.HandleFunc("/api/nodes", ws.NodesList)
	mux.PathPrefix("/api/node/").HandlerFunc(ws.NodeFlags).Methods("GET")

	mux.PathPrefix("/api/flag/").HandlerFunc(ws.SetFlag).Methods("PUT")
	mux.PathPrefix("/api/flag/").HandlerFunc(ws.DelFlag).Methods("DELETE")

	mux.HandleFunc("/upload/", ws.Upload)
	mux.HandleFunc("/files", ws.Files).Methods("GET")
	mux.HandleFunc("/files", ws.DeleteFile).Methods("DELETE")
	mux.PathPrefix("/files/").Handler(http.StripPrefix("/files/",
		http.FileServer(http.Dir(filepath.Join(ws.ds.WorkspacePath(), "files")))))

	mux.PathPrefix("/ui/").Handler(http.FileServer(FS(false)))

	return mux
}

//ServeWeb serves api of Blacksmith and a ui connected to that api
func ServeWeb(ds datasource.DataSource, listenAddr net.TCPAddr) error {
	r := &webServer{ds: ds}
	loggedRouter := handlers.LoggingHandler(os.Stdout, r.Handler())
	s := &http.Server{
		Addr:    listenAddr.String(),
		Handler: loggedRouter,
	}

	return s.ListenAndServe()
}
