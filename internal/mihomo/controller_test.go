package mihomo

import (
	"context"
	"encoding/json"\n\t"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

func TestControllerHealthAndReload(t *testing.T){
	var reloadPath string
	server:=httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter,r *http.Request){
		if r.Header.Get("Authorization")!="Bearer secret"{t.Errorf("missing auth")}
		switch{
		case r.Method=="GET"&&r.URL.Path=="/version": w.Header().Set("Content-Type","application/json");_,_=w.Write([]byte(`{"version":"1.19.0"}`))
		case r.Method=="PUT"&&r.URL.Path=="/configs":
			if r.URL.Query().Get("force")!="true"{t.Errorf("force query missing")}
			var body struct{Path string `json:"path"`};_ = json.NewDecoder(r.Body).Decode(&body);reloadPath=body.Path;w.WriteHeader(http.StatusNoContent)
		default:http.NotFound(w,r)
		}
	}))
	defer server.Close()
	controller,err:=NewController(server.URL,"secret","/root/.config/mihomo/config.yaml");if err!=nil{t.Fatal(err)}
	if err:=controller.Healthy(context.Background());err!=nil{t.Fatal(err)}
	if err:=controller.Reload(context.Background());err!=nil{t.Fatal(err)}
	if reloadPath!="/root/.config/mihomo/config.yaml"{t.Fatalf("path=%q",reloadPath)}
}

func TestControllerValidatesYAMLBeforeReload(t *testing.T){
	controller,err:=NewController("http://127.0.0.1:9090","","/config.yaml");if err!=nil{t.Fatal(err)}
	path:=filepath.Join(t.TempDir(),"config.yaml")
	if err:=os.WriteFile(path,[]byte("proxies: ["),0o600);err!=nil{t.Fatal(err)}
	if err:=controller.Validate(context.Background(),path);err==nil{t.Fatal("expected YAML error")}
}
