package api

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/foxc888/foxos/internal/domain"
	"github.com/foxc888/foxos/internal/routeros"
)
type fakeL2TP struct{}
func(fakeL2TP)L2TPClients(context.Context)([]routeros.L2TPClient,error){return []routeros.L2TPClient{{ID:"*1",Name:"JP-L2TP",ConnectTo:"vpn.example.com",User:"fox",Running:"true",Comment:"foxos:l2tp:japan"}},nil}
func TestL2TPResponseHasNoPassword(t *testing.T){
	const token="01234567890123456789012345678901"
	app,err:=New(&memoryNodes{nodes:map[string]domain.Node{}},token);if err!=nil{t.Fatal(err)}
	mux:=http.NewServeMux();app.RegisterL2TP(mux,fakeL2TP{})
	request:=httptest.NewRequest("GET","/api/v1/routeros/l2tp",nil);request.Header.Set("Authorization","Bearer "+token)
	response:=httptest.NewRecorder();mux.ServeHTTP(response,request)
	if response.Code!=200{t.Fatalf("status=%d",response.Code)}
	if strings.Contains(strings.ToLower(response.Body.String()),"password"){t.Fatalf("response=%s",response.Body.String())}
	if !strings.Contains(response.Body.String(),`"ownedByFoxos":true`){t.Fatalf("response=%s",response.Body.String())}
}
