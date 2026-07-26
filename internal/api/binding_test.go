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
type fakeLeases struct{leases []routeros.Lease}
func(f fakeLeases)Leases(context.Context)([]routeros.Lease,error){return f.leases,nil}

func TestBindingPlanDoesNotWrite(t *testing.T){
	const token="01234567890123456789012345678901"
	app,err:=New(&memoryNodes{nodes:map[string]domain.Node{}},token);if err!=nil{t.Fatal(err)}
	mux:=http.NewServeMux();app.RegisterBindingPlan(mux,fakeLeases{leases:[]routeros.Lease{{ID:"*1",Address:"10.0.0.19",MACAddress:"AA:BB:CC:DD:EE:FF",Dynamic:"true"}}})
	body:=`{"id":"phone","name":"iPhone","macAddress":"AA:BB:CC:DD:EE:FF","staticIp":"10.0.0.20","dhcpServer":"dhcp-lan","egress":"direct"}`
	request:=httptest.NewRequest("POST","/api/v1/routeros/plans/device-binding",strings.NewReader(body));request.Header.Set("Authorization","Bearer "+token)
	response:=httptest.NewRecorder();mux.ServeHTTP(response,request)
	if response.Code!=http.StatusOK{t.Fatalf("status=%d body=%s",response.Code,response.Body.String())}
	if !strings.Contains(response.Body.String(),`"requiresConfirmation":true`){t.Fatalf("body=%s",response.Body.String())}
}
