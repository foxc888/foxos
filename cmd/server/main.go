package main

import (
	"encoding/json"
	"flag"
	"log"
	"net/http"
	"os"
	"time"

	"github.com/foxc888/foxos/internal/api"
	"github.com/foxc888/foxos/internal/config"
	"github.com/foxc888/foxos/internal/mihomo"
	"github.com/foxc888/foxos/internal/routeros"
	"github.com/foxc888/foxos/internal/store/sqlite"
)

var version="dev"
type health struct{Status string `json:"status"`;Version string `json:"version"`;Time string `json:"time"`}

func main(){
	address:=flag.String("listen",":8090","HTTP listen address")
	staticDir:=flag.String("static","web/dist","built frontend directory")
	databasePath:=flag.String("database","data/foxos.db","SQLite database path")
	flag.Parse()
	runtimeConfig,err:=config.Load();if err!=nil{log.Fatal(err)}
	store,err:=sqlite.Open(*databasePath);if err!=nil{log.Fatal(err)};defer store.Close()
	app,err:=api.New(store,runtimeConfig.APIToken);if err!=nil{log.Fatal(err)}

	var ros api.RouterOSReader
	if runtimeConfig.RouterOS.URL!=""{
		client,err:=routeros.NewClient(runtimeConfig.RouterOS.URL,runtimeConfig.RouterOS.Username,runtimeConfig.RouterOS.Password);if err!=nil{log.Fatal(err)}
		ros=client
	}
	var clash api.MihomoReader
	if runtimeConfig.Mihomo.URL!=""{
		controller,err:=mihomo.NewController(runtimeConfig.Mihomo.URL,runtimeConfig.Mihomo.Secret,runtimeConfig.Mihomo.RuntimeConfigPath);if err!=nil{log.Fatal(err)}
		clash=controller
	}

	mux:=http.NewServeMux()
	mux.HandleFunc("GET /api/v1/health/live",func(w http.ResponseWriter,_ *http.Request){writeJSON(w,200,health{Status:"ok",Version:version,Time:time.Now().UTC().Format(time.RFC3339)})})
	mux.HandleFunc("GET /api/v1/health/ready",func(w http.ResponseWriter,_ *http.Request){writeJSON(w,200,health{Status:"ready",Version:version,Time:time.Now().UTC().Format(time.RFC3339)})})
	app.Register(mux);app.RegisterDevicePolicies(mux,store);app.RegisterStatus(mux,ros,clash);app.RegisterBindingPlan(mux,leases)
	if info,err:=os.Stat(*staticDir);err==nil&&info.IsDir(){mux.Handle("/",http.FileServer(http.Dir(*staticDir)))}
	server:=&http.Server{Addr:*address,Handler:securityHeaders(mux),ReadHeaderTimeout:5*time.Second,ReadTimeout:15*time.Second,WriteTimeout:30*time.Second,IdleTimeout:60*time.Second}
	log.Printf("FoxOS %s listening on %s",version,*address);log.Fatal(server.ListenAndServe())
}
func writeJSON(w http.ResponseWriter,status int,value any){w.Header().Set("Content-Type","application/json; charset=utf-8");w.WriteHeader(status);_ = json.NewEncoder(w).Encode(value)}
func securityHeaders(next http.Handler)http.Handler{return http.HandlerFunc(func(w http.ResponseWriter,r *http.Request){w.Header().Set("X-Content-Type-Options","nosniff");w.Header().Set("X-Frame-Options","DENY");w.Header().Set("Referrer-Policy","no-referrer");next.ServeHTTP(w,r)})}
