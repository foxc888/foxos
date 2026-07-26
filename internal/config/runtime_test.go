package config

import "testing"

func TestLoadRequiresToken(t *testing.T){
	t.Setenv("FOXOS_API_TOKEN","")
	if _,err:=Load();err==nil{t.Fatal("expected token error")}
}
func TestLoadOptionalAdapters(t *testing.T){
	t.Setenv("FOXOS_API_TOKEN","01234567890123456789012345678901")
	cfg,err:=Load();if err!=nil{t.Fatal(err)}
	if cfg.RouterOS.URL!=""||cfg.Mihomo.URL!=""{t.Fatalf("cfg=%+v",cfg)}
}
func TestLoadRejectsCredentialsInURL(t *testing.T){
	t.Setenv("FOXOS_API_TOKEN","01234567890123456789012345678901")
	t.Setenv("FOXOS_ROUTEROS_URL","http://admin:secret@10.0.0.1")
	t.Setenv("FOXOS_ROUTEROS_USERNAME","admin")
	t.Setenv("FOXOS_ROUTEROS_PASSWORD","secret")
	if _,err:=Load();err==nil{t.Fatal("expected URL error")}
}
